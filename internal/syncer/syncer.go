package syncer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/enrichment"
	fbadapter "github.com/egori/facebook-aggregator/internal/facebook"
	"github.com/egori/facebook-aggregator/internal/parser"
	"github.com/egori/facebook-aggregator/internal/ranking"
	"github.com/egori/facebook-aggregator/internal/storage"
)

type Service struct {
	store        *storage.Store
	fb           fbadapter.Adapter
	parser       *parser.Parser
	rank         ranking.Engine
	extractor    *enrichment.Service
	log          *slog.Logger
	concurrency  int
	trigger      chan triggerRequest
	mu           sync.Mutex
	running      map[int64]bool
	pendingForce map[int64]int
}

type triggerRequest struct {
	groupID  int64
	maxPosts int
}

func New(store *storage.Store, fb fbadapter.Adapter, p *parser.Parser, r ranking.Engine, extractor *enrichment.Service, log *slog.Logger, concurrency int) *Service {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Service{store: store, fb: fb, parser: p, rank: r, extractor: extractor, log: log, concurrency: concurrency, trigger: make(chan triggerRequest, 100), running: map[int64]bool{}, pendingForce: map[int64]int{}}
}
func (s *Service) Trigger(groupID int64) bool {
	select {
	case s.trigger <- triggerRequest{groupID: groupID}:
		return true
	default:
		return false
	}
}

// ForceSyncAll bypasses next_poll_at for every enabled group. maxPosts applies
// only to this recovery pass; normal incremental polling remains unchanged.
func (s *Service) ForceSyncAll(ctx context.Context, maxPosts int) (int, error) {
	// Do not make groups due here. Doing so lets the polling ticker race the
	// recovery triggers and start an uncapped sync before the maxPosts pass.
	// Explicit triggers already bypass next_poll_at.
	groups, err := s.store.EnabledGroups(ctx)
	if err != nil {
		return 0, err
	}
	for _, group := range groups {
		select {
		case s.trigger <- triggerRequest{groupID: group.ID, maxPosts: maxPosts}:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return len(groups), nil
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	sem := make(chan struct{}, s.concurrency)
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-s.trigger:
			g, err := s.store.Group(ctx, request.groupID)
			if err == nil && (request.maxPosts == 0 || g.Enabled) {
				s.launch(ctx, sem, g, request.maxPosts)
			}
		case <-ticker.C:
			groups, err := s.store.DueGroups(ctx, s.concurrency*2)
			if err != nil {
				s.log.Error("load due groups", "error", err)
				continue
			}
			for _, g := range groups {
				s.launch(ctx, sem, g, 0)
			}
		}
	}
}
func (s *Service) launch(ctx context.Context, sem chan struct{}, g domain.Group, maxPosts int) {
	s.mu.Lock()
	if s.running[g.ID] {
		if maxPosts > s.pendingForce[g.ID] {
			s.pendingForce[g.ID] = maxPosts
		}
		s.mu.Unlock()
		return
	}
	s.running[g.ID] = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			pending := s.pendingForce[g.ID]
			delete(s.pendingForce, g.ID)
			delete(s.running, g.ID)
			s.mu.Unlock()
			if pending > 0 {
				select {
				case s.trigger <- triggerRequest{groupID: g.ID, maxPosts: pending}:
				case <-ctx.Done():
				}
			}
		}()
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			return
		}
		if _, err := s.syncGroup(ctx, g, maxPosts); err != nil {
			s.log.Warn("group sync failed", "group_id", g.ID, "error", err)
		}
	}()
}
func (s *Service) SyncGroup(ctx context.Context, g domain.Group) (domain.GroupSyncResult, error) {
	return s.syncGroup(ctx, g, 0)
}

func (s *Service) syncGroup(ctx context.Context, g domain.Group, maxPosts int) (domain.GroupSyncResult, error) {
	runID, err := s.store.SyncStarted(ctx, g.ID)
	if err != nil {
		return domain.GroupSyncResult{}, err
	}
	var result domain.GroupSyncResult
	defer func() { s.store.SyncFinished(context.WithoutCancel(ctx), runID, g.ID, result, err, g.PollingInterval) }()
	if g.FacebookID == "" {
		id, name, url, e := s.fb.ResolveGroup(ctx, g.URL)
		if e != nil {
			err = e
			return result, err
		}
		if e = s.store.ResolveGroup(ctx, g.ID, id, name, url); e != nil {
			err = e
			return result, err
		}
		g.FacebookID = id
		if g.Name == "" {
			g.Name = name
		}
	}
	fetched, e := s.fb.FetchRecent(ctx, fetchRequest(g, maxPosts))
	if e != nil {
		err = e
		return result, err
	}
	fetched.Posts = uniquePosts(fetched.Posts)
	result.Fetched = len(fetched.Posts)
	result.NewestID = fetched.NewestID
	result.NewestAt = fetched.NewestAt
	result.ReachedOld = fetched.ReachedOld
	// Process oldest first so comparable statistics evolve monotonically.
	for i := len(fetched.Posts) - 1; i >= 0; i-- {
		post := fetched.Posts[i]
		if strings.TrimSpace(post.Text) == "" && len(post.MediaURLs) == 0 {
			continue
		}
		l := s.parser.Parse(post, g.ID, g.Name)
		if s.extractor != nil {
			enriched, _, enrichErr := s.extractor.Apply(ctx, l)
			l = enriched
			if enrichErr != nil {
				s.log.Warn("LLM extraction issue; continuing rule pipeline", "post_id", post.ID, "error", enrichErr)
			}
		}
		b, bErr := s.store.Benchmarks(ctx, l)
		if bErr != nil && !errors.Is(bErr, context.Canceled) {
			s.log.Debug("benchmarks unavailable", "error", bErr)
		}
		l.DealScore, l.ScoreConfidence = s.rank.Score(l, b, time.Now())
		inserted, iErr := s.store.InsertListing(ctx, post, l)
		if iErr != nil {
			err = iErr
			return result, err
		}
		if inserted {
			result.Inserted++
		}
	}
	return result, nil
}
func fetchRequest(g domain.Group, maxPosts int) fbadapter.FetchRequest {
	return fbadapter.FetchRequest{GroupID: g.FacebookID, StopPostID: g.LastPostID, StopBefore: timeOrZero(g.LastPostAt), MaxPages: 10, MaxPosts: maxPosts, Overlap: 6 * time.Hour}
}
func uniquePosts(in []domain.FacebookPost) []domain.FacebookPost {
	seen := map[string]bool{}
	out := make([]domain.FacebookPost, 0, len(in))
	for _, p := range in {
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	return out
}
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
