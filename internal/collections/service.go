package collections

import (
	"context"
	"fmt"
	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/llm"
	"github.com/egori/facebook-aggregator/internal/storage"
	"sync"
	"time"
)

type cached struct {
	items []domain.CollectionItem
	until time.Time
}
type Service struct {
	store    *storage.Store
	provider func(context.Context) llm.Provider
	mu       sync.Mutex
	cache    map[int]cached
}

func New(s *storage.Store, p func(context.Context) llm.Provider) *Service {
	return &Service{store: s, provider: p, cache: map[int]cached{}}
}
func (s *Service) Get(ctx context.Context, user int64, days int) ([]domain.CollectionItem, error) {
	if days != 1 && days != 7 && days != 30 {
		days = 7
	}
	s.mu.Lock()
	c, ok := s.cache[days]
	s.mu.Unlock()
	if ok && time.Now().Before(c.until) {
		return c.items, nil
	}
	after := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	page, e := s.store.Search(ctx, user, domain.SearchFilter{FreshAfter: &after, Sort: "score", Limit: 40})
	if e != nil {
		return nil, e
	}
	items := make([]domain.CollectionItem, 0, 15)
	provider := s.provider(ctx)
	choices, e := provider.Curate(ctx, page.Items, 15)
	if e == nil && len(choices) > 0 {
		byID := map[int64]domain.Listing{}
		for _, x := range page.Items {
			byID[x.ID] = x
		}
		for i, x := range choices {
			if l, ok := byID[x.ListingID]; ok {
				items = append(items, domain.CollectionItem{Listing: l, Reason: x.Reason, Rank: i + 1})
			}
		}
	}
	if len(items) == 0 {
		for i, l := range page.Items {
			if i >= 15 {
				break
			}
			reason := "Хорошее соотношение цены, характеристик и свежести по локальному рейтингу."
			if l.ScoreConfidence < .45 {
				reason = "Перспективный вариант, но данных для уверенной оценки пока немного."
			}
			items = append(items, domain.CollectionItem{Listing: l, Reason: reason, Rank: i + 1})
		}
	}
	s.mu.Lock()
	s.cache[days] = cached{items, time.Now().Add(10 * time.Minute)}
	s.mu.Unlock()
	return items, nil
}
func Title(days int) string {
	if days == 1 {
		return "сегодня"
	}
	return fmt.Sprintf("за последние %d дней", days)
}
