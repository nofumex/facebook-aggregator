package ranking

import (
	"github.com/egori/facebook-aggregator/internal/domain"
	"math"
	"time"
)

type Benchmarks struct {
	MedianRent, MedianPriceM2 float64
	SimilarCount              int
	HistoricalTrend           float64
}
type Weights struct{ Value, PriceM2, Freshness, Completeness, Amenities, Beach, Furnishing, Foreigner, Utilities, Deposit, Rarity float64 }
type Engine struct{ W Weights }

func New() Engine {
	return Engine{Weights{Value: 32, PriceM2: 14, Freshness: 12, Completeness: 10, Amenities: 8, Beach: 6, Furnishing: 5, Foreigner: 4, Utilities: 4, Deposit: 3, Rarity: 2}}
}

func (e Engine) Score(l domain.Listing, b Benchmarks, now time.Time) (float64, float64) {
	score := 50.0
	evidence := 0.0
	if l.RentMin != nil && b.MedianRent > 0 {
		ratio := float64(*l.RentMin) / b.MedianRent
		score += clamp((1-ratio)*e.W.Value, -e.W.Value*.55, e.W.Value)
		evidence += .24
	}
	if l.RentMin != nil && l.AreaM2 != nil && *l.AreaM2 > 0 && b.MedianPriceM2 > 0 {
		ppm := float64(*l.RentMin) / *l.AreaM2
		score += clamp((1-ppm/b.MedianPriceM2)*e.W.PriceM2, -e.W.PriceM2*.5, e.W.PriceM2)
		evidence += .18
	}
	age := now.Sub(l.PublishedAt)
	if age < 0 {
		age = 0
	}
	score += e.W.Freshness * math.Exp(-age.Hours()/(24*7))
	evidence += .08
	fields := 0
	for _, ok := range []bool{l.RentMin != nil, l.Bedrooms != nil, l.AreaM2 != nil, l.District != "", l.PropertyType != "", l.Furnished != "", len(l.MediaURLs) > 0} {
		if ok {
			fields++
		}
	}
	score += e.W.Completeness * (float64(fields)/7 - .45)
	evidence += float64(fields) / 7 * .16
	amenityCount := 0
	for _, v := range l.Amenities {
		if v {
			amenityCount++
		}
	}
	score += math.Min(e.W.Amenities, float64(amenityCount)*1.35)
	if l.NearBeach != nil && *l.NearBeach {
		bonus := e.W.Beach
		if l.BeachDistanceM != nil {
			bonus *= clamp(1-float64(*l.BeachDistanceM)/3000, .25, 1)
		}
		score += bonus
		evidence += .05
	}
	if l.Furnished == "full" {
		score += e.W.Furnishing
	} else if l.Furnished == "basic" {
		score += e.W.Furnishing * .45
	}
	if l.ForeignersAccepted != nil && *l.ForeignersAccepted {
		score += e.W.Foreigner
		evidence += .03
	}
	if gov, ok := l.Utilities["government_rate"].(bool); ok && gov {
		score += e.W.Utilities
		evidence += .03
	}
	if l.DepositAmount != nil && l.RentMin != nil && *l.RentMin > 0 {
		months := float64(*l.DepositAmount) / float64(*l.RentMin)
		score += clamp((2-months)*e.W.Deposit, -e.W.Deposit, e.W.Deposit)
	}
	if b.SimilarCount >= 8 {
		evidence += .18
		if b.SimilarCount < 25 {
			score += e.W.Rarity * .5
		}
	}
	// With sparse comparables, shrink toward a neutral score instead of
	// pretending an unusually cheap incomplete post is certainly a bargain.
	confidence := clamp(evidence, .15, 1)
	score = 50 + (score-50)*(.45+.55*confidence)
	return math.Round(clamp(score, 0, 100)*10) / 10, math.Round(confidence*100) / 100
}
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
