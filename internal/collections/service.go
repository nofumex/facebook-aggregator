package collections

import (
	"context"
	"fmt"
	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/llm"
	"github.com/egori/facebook-aggregator/internal/storage"
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
}

func New(s *storage.Store, p func(context.Context) llm.Provider) *Service {
	return &Service{store: s, provider: p, cache: map[cacheKey]cached{}}
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
	page, e := s.store.Search(ctx, user, domain.SearchFilter{FreshAfter: &after, Sort: "score", Limit: 50})
	if e != nil {
		return nil, e
	}
	candidates := Eligible(page.Items)
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
	return items, nil
}
func Title(days int) string {
	if days == 1 {
		return "сегодня"
	}
	return fmt.Sprintf("за последние %d дней", days)
}
