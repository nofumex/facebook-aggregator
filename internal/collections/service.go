package collections

import (
	"context"
	"fmt"
	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/llm"
	"github.com/egori/facebook-aggregator/internal/ranking"
	"github.com/egori/facebook-aggregator/internal/storage"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type cached struct {
	items []domain.CollectionItem
	until time.Time
}
type cacheKey struct {
	user int64
	days int
}
type Service struct {
	store    *storage.Store
	provider func(context.Context) llm.Provider
	mu       sync.Mutex
	cache    map[cacheKey]cached
	rank     ranking.Engine
	log      *slog.Logger
}

func New(s *storage.Store, p func(context.Context) llm.Provider) *Service {
	return &Service{store: s, provider: p, cache: map[cacheKey]cached{}, rank: ranking.New(), log: slog.Default()}
}
func NewWithRanking(s *storage.Store, p func(context.Context) llm.Provider, r ranking.Engine, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: s, provider: p, cache: map[cacheKey]cached{}, rank: r, log: log}
}
func (s *Service) Get(ctx context.Context, user int64, days int) ([]domain.CollectionItem, error) {
	if days != 1 && days != 7 && days != 30 {
		days = 7
	}
	key := cacheKey{user: user, days: days}
	s.mu.Lock()
	c, ok := s.cache[key]
	s.mu.Unlock()
	if ok && time.Now().Before(c.until) {
		return c.items, nil
	}
	after := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	reranked, e := s.store.RerankPeriod(ctx, after, s.rank, 500)
	if e != nil {
		return nil, e
	}
	const pageSize = 250
	var candidates []domain.Listing
	totalPeriod, normalized := 0, 0
	for offset := 0; ; offset += pageSize {
		page, e := s.store.Search(ctx, user, domain.SearchFilter{FreshAfter: &after, Sort: "score", Limit: pageSize, Offset: offset})
		if e != nil {
			return nil, e
		}
		if offset == 0 {
			totalPeriod = page.Total
		}
		for _, x := range page.Items {
			if x.ExtractionStatus == "success" {
				normalized++
			}
			if IsEligible(x) {
				candidates = append(candidates, x)
			}
		}
		if len(page.Items) < pageSize || len(candidates) >= 200 || page.Items[len(page.Items)-1].DealScore < MinimumDealScore {
			break
		}
	}
	items := make([]domain.CollectionItem, 0, 15)
	provider := s.provider(ctx)
	choices, curateErr := provider.Curate(ctx, candidates, 15)
	if curateErr == nil && provider.Name() != "disabled" {
		byID := map[int64]domain.Listing{}
		for _, x := range candidates {
			byID[x.ID] = x
		}
		for _, x := range choices {
			if l, ok := byID[x.ListingID]; ok && strings.TrimSpace(x.Reason) != "" && len(items) < 15 {
				i := len(items)
				items = append(items, domain.CollectionItem{Listing: l, Reason: x.Reason, Rank: i + 1})
			}
		}
	} else {
		for i, l := range candidates {
			if i >= 15 {
				break
			}
			items = append(items, domain.CollectionItem{Listing: l, Reason: localReason(l), Rank: i + 1})
		}
	}
	s.mu.Lock()
	s.cache[key] = cached{items, time.Now().Add(10 * time.Minute)}
	s.mu.Unlock()
	attrs := []any{"days", days, "total_period", totalPeriod, "normalized", normalized, "quality_passed", len(candidates), "reranked", reranked, "shortlist", min(len(candidates), 40), "selected", len(items)}
	if len(items) > 0 {
		oldest, newest := items[0].PublishedAt, items[0].PublishedAt
		for _, x := range items {
			if x.PublishedAt.Before(oldest) {
				oldest = x.PublishedAt
			}
			if x.PublishedAt.After(newest) {
				newest = x.PublishedAt
			}
		}
		attrs = append(attrs, "oldest_selected", oldest, "newest_selected", newest)
	}
	s.log.Info("collection built", attrs...)
	return items, nil
}
func Title(days int) string {
	if days == 1 {
		return "сегодня"
	}
	return fmt.Sprintf("за последние %d дней", days)
}
