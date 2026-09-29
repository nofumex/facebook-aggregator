package ranking

import (
	"github.com/nofumex/telegram-aggregator/internal/domain"
	"testing"
	"time"
)

func TestNhaTrangOceanusLowPriceSynergy(t *testing.T) {
	price, deposit, beds, lease, oceanus := int64(7_000_000), int64(7_000_000), 1, 3, true
	l := domain.Listing{City: domain.CityNhaTrang, Zone: domain.ZoneNorth, PropertyType: "apartment", RentMin: &price, DepositAmount: &deposit, Bedrooms: &beds, LeaseMonths: &lease, IsOceanus: &oceanus, Confidence: domain.Confidence{"price": 1, "district": 1, "property_type": 1, "bedrooms": 1}}
	score, _ := ScoreNhaTrang(l, 20, DefaultNhaTrangConfig())
	if score < 97 || score > 100 {
		t.Fatalf("score=%v, want 97..100", score)
	}
}

func TestNhaTrangPriceAloneIsNotPerfect(t *testing.T) {
	price := int64(6_000_000)
	score, _ := ScoreNhaTrang(domain.Listing{City: domain.CityNhaTrang, RentMin: &price}, 0, DefaultNhaTrangConfig())
	if score >= 100 {
		t.Fatalf("price alone scored %v", score)
	}
}

func TestDaNangRankingUnchangedByCityRouting(t *testing.T) {
	price := int64(8_000_000)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := domain.Listing{City: domain.CityDaNang, RentMin: &price, PublishedAt: now}
	e := New()
	a, ac := e.Score(l, Benchmarks{MedianRent: 10_000_000}, now)
	l.City = ""
	b, bc := e.Score(l, Benchmarks{MedianRent: 10_000_000}, now)
	if a != b || ac != bc {
		t.Fatalf("Da Nang regression: %v/%v vs %v/%v", a, ac, b, bc)
	}
}
