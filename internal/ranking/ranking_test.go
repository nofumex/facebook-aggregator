package ranking

import (
	"github.com/egori/facebook-aggregator/internal/domain"
	"testing"
	"time"
)

func i64(v int64) *int64   { return &v }
func i(v int) *int         { return &v }
func f(v float64) *float64 { return &v }
func TestValueForMoney(t *testing.T) {
	e := New()
	now := time.Now()
	apt := domain.Listing{RentMin: i64(5_000_000), Bedrooms: i(2), AreaM2: f(50), District: "Sơn Trà", PropertyType: "apartment", PublishedAt: now, Amenities: map[string]bool{"balcony": true}, Utilities: map[string]any{}}
	room := domain.Listing{RentMin: i64(3_500_000), AreaM2: f(18), PropertyType: "room", PublishedAt: now, Amenities: map[string]bool{}, Utilities: map[string]any{}}
	a, _ := e.Score(apt, Benchmarks{MedianRent: 9_000_000, MedianPriceM2: 180_000, SimilarCount: 30}, now)
	b, _ := e.Score(room, Benchmarks{MedianRent: 3_700_000, MedianPriceM2: 190_000, SimilarCount: 30}, now)
	if a <= b {
		t.Fatalf("apartment %.1f must outrank room %.1f", a, b)
	}
}
func TestSparseDataIsCautious(t *testing.T) {
	e := New()
	s, c := e.Score(domain.Listing{PublishedAt: time.Now(), Amenities: map[string]bool{}, Utilities: map[string]any{}}, Benchmarks{}, time.Now())
	if s < 35 || s > 70 || c > .5 {
		t.Fatalf("score %.1f confidence %.2f", s, c)
	}
}
