package collections

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/llm"
	"github.com/egori/facebook-aggregator/internal/ranking"
	"github.com/egori/facebook-aggregator/internal/storage"
)

var ErrSnapshotNotReady = errors.New("collection snapshot is not ready")

type collectionStore interface {
	Search(context.Context, int64, domain.SearchFilter) (domain.SearchPage, error)
}

type snapshot struct {
	items   []domain.CollectionItem
	builtAt time.Time
}

// Service serves immutable collection snapshots to Telegram. All database,
// ranking-dependent selection and optional LLM curation happens in Run.
type Service struct {
	store     collectionStore
	provider  func(context.Context) llm.Provider
	mu        sync.RWMutex
	snapshots map[int]snapshot
	log       *slog.Logger
}

func New(s *storage.Store, p func(context.Context) llm.Provider) *Service {
	return newService(s, p, slog.Default())
}

// The ranking argument remains for source compatibility. Scores are maintained
// by the dedicated reranking worker and are never recalculated here.
func NewWithRanking(s *storage.Store, p func(context.Context) llm.Provider, _ ranking.Engine, log *slog.Logger) *Service {
	return newService(s, p, log)
}

func newService(s collectionStore, p func(context.Context) llm.Provider, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: s, provider: p, snapshots: map[int]snapshot{}, log: log}
}

// Get is UI-only: it never performs database work, reranking or LLM calls.
func (s *Service) Get(_ context.Context, _ int64, days int) ([]domain.CollectionItem, error) {
	days = normalizedDays(days)
	s.mu.RLock()
	current, ok := s.snapshots[days]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrSnapshotNotReady
	}
	return append([]domain.CollectionItem(nil), current.items...), nil
}

// Run refreshes 1/7/30-day snapshots sequentially, keeping database pressure
// bounded. A failed refresh never replaces the last successfully built value.
func (s *Service) Run(ctx context.Context, interval, refreshTimeout time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	if refreshTimeout <= 0 {
		refreshTimeout = 90 * time.Second
	}
	refreshAll := func() {
		for _, days := range []int{1, 7, 30} {
			if ctx.Err() != nil {
				return
			}
			refreshCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
			err := s.refreshPeriod(refreshCtx, days)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					s.log.Warn("collection snapshot refresh failed", "days", days, "error", err)
				}
				continue
			}
		}
	}

	refreshAll()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshAll()
		}
	}
}

func (s *Service) refreshPeriod(ctx context.Context, days int) error {
	items, stats, err := s.build(ctx, days)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.snapshots[normalizedDays(days)] = snapshot{items: items, builtAt: time.Now().UTC()}
	s.mu.Unlock()
	s.logBuilt(days, items, stats)
	return nil
}

type buildStats struct {
	totalPeriod, normalized, qualityPassed int
}

func (s *Service) build(ctx context.Context, days int) ([]domain.CollectionItem, buildStats, error) {
	after := time.Now().Add(-time.Duration(normalizedDays(days)) * 24 * time.Hour)
	const pageSize = 250
	var candidates []domain.Listing
	var stats buildStats
	for offset := 0; ; offset += pageSize {
		page, err := s.store.Search(ctx, 0, domain.SearchFilter{FreshAfter: &after, Sort: "score", Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, stats, err
		}
		if offset == 0 {
			stats.totalPeriod = page.Total
		}
		for _, listing := range page.Items {
			if listing.ExtractionStatus == "success" {
				stats.normalized++
			}
			if IsEligible(listing) {
				candidates = append(candidates, listing)
			}
		}
		if len(page.Items) < pageSize || len(candidates) >= 200 || page.Items[len(page.Items)-1].DealScore < MinimumDealScore {
			break
		}
	}
	stats.qualityPassed = len(candidates)

	items := make([]domain.CollectionItem, 0, 15)
	provider := s.provider(ctx)
	choices, curateErr := provider.Curate(ctx, candidates, 15)
	if curateErr == nil && provider.Name() != "disabled" {
		byID := make(map[int64]domain.Listing, len(candidates))
		for _, listing := range candidates {
			byID[listing.ID] = listing
		}
		for _, choice := range choices {
			if listing, ok := byID[choice.ListingID]; ok && strings.TrimSpace(choice.Reason) != "" && len(items) < 15 {
				items = append(items, domain.CollectionItem{Listing: listing, Reason: choice.Reason, Rank: len(items) + 1})
			}
		}
	} else {
		if curateErr != nil && provider.Name() != "disabled" {
			s.log.Warn("collection curator failed; using local quality order", "days", days, "error", curateErr)
		}
		for i, listing := range candidates {
			if i >= 15 {
				break
			}
			items = append(items, domain.CollectionItem{Listing: listing, Reason: localReason(listing), Rank: i + 1})
		}
	}
	return items, stats, nil
}

func (s *Service) logBuilt(days int, items []domain.CollectionItem, stats buildStats) {
	attrs := []any{"days", days, "total_period", stats.totalPeriod, "normalized", stats.normalized, "quality_passed", stats.qualityPassed, "shortlist", min(stats.qualityPassed, 40), "selected", len(items)}
	if len(items) > 0 {
		oldest, newest := items[0].PublishedAt, items[0].PublishedAt
		for _, item := range items {
			if item.PublishedAt.Before(oldest) {
				oldest = item.PublishedAt
			}
			if item.PublishedAt.After(newest) {
				newest = item.PublishedAt
			}
		}
		attrs = append(attrs, "oldest_selected", oldest, "newest_selected", newest)
	}
	s.log.Info("collection snapshot built", attrs...)
}

func normalizedDays(days int) int {
	if days != 1 && days != 7 && days != 30 {
		return 7
	}
	return days
}

func Title(days int) string {
	if days == 1 {
		return "сегодня"
	}
	return fmt.Sprintf("за последние %d дней", days)
}
