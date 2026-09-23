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

func TestStrongThreeDayOldListingBeatsWeakFreshListing(t *testing.T) {
	e, now := New(), time.Now()
	strong := domain.Listing{RentMin: i64(5_000_000), Bedrooms: i(1), AreaM2: f(45), District: "Son Tra", PropertyType: "apartment", Furnished: "full", PublishedAt: now.Add(-72 * time.Hour), Confidence: domain.Confidence{"price": .95, "bedrooms": .9, "area_m2": .9, "district": .9, "property_type": .9, "furnished": .9}, Amenities: map[string]bool{"balcony": true}, Utilities: map[string]any{}}
	weak := domain.Listing{RentMin: i64(6_800_000), Bedrooms: i(1), AreaM2: f(45), District: "Son Tra", PropertyType: "apartment", PublishedAt: now.Add(-20 * time.Minute), Confidence: domain.Confidence{"price": .8}, Amenities: map[string]bool{}, Utilities: map[string]any{}}
	b := Benchmarks{MedianRent: 7_000_000, MedianPriceM2: 155_000, SimilarCount: 40}
	aScore, _ := e.Score(strong, b, now)
	bScore, _ := e.Score(weak, b, now)
	if aScore <= bScore {
		t.Fatalf("older strong %.1f should beat fresh weak %.1f", aScore, bScore)
	}
}

func TestScoreChangesWhenComparablesChange(t *testing.T) {
	e, now := New(), time.Now()
	l := domain.Listing{RentMin: i64(5_000_000), Bedrooms: i(1), AreaM2: f(40), District: "Son Tra", PropertyType: "apartment", PublishedAt: now, Confidence: domain.Confidence{"price": .9}, Amenities: map[string]bool{}, Utilities: map[string]any{}}
	before, _ := e.Score(l, Benchmarks{MedianRent: 5_100_000, MedianPriceM2: 127_500, SimilarCount: 8}, now)
	after, _ := e.Score(l, Benchmarks{MedianRent: 7_000_000, MedianPriceM2: 175_000, SimilarCount: 40}, now)
	if after <= before {
		t.Fatalf("reranking did not react to new comparables: %.1f -> %.1f", before, after)
	}
}
