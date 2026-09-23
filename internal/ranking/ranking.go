package ranking

import (
	"math"
	"time"

	"github.com/egori/facebook-aggregator/internal/domain"
)

type Benchmarks struct {
	MedianRent, MedianPriceM2 float64
	SimilarCount              int
	HistoricalTrend           float64
}
type Weights struct{ RelativeRent, PriceM2, Area, Bedrooms, District, Furnishing, Beach, Amenities, Utilities, Freshness, Evidence float64 }
type RankingConfig struct {
	Base                          float64
	Weights                       Weights
	District, Bedroom, AreaBucket map[string]float64
	Furnishing                    map[string]float64
}
type Engine struct{ Config RankingConfig }

func DefaultConfig() RankingConfig {
	return RankingConfig{
		Base:     50,
		Weights:  Weights{RelativeRent: 26, PriceM2: 12, Area: 3, Bedrooms: 3, District: 3, Furnishing: 4, Beach: 4, Amenities: 4, Utilities: 2, Freshness: 3, Evidence: 5},
		District: map[string]float64{}, Bedroom: map[string]float64{}, AreaBucket: map[string]float64{},
		Furnishing: map[string]float64{"full": .5, "partial": .2, "none": 0},
	}
}
func New() Engine                          { return Engine{Config: DefaultConfig()} }
func NewWithConfig(c RankingConfig) Engine { return Engine{Config: c} }

// Score is deterministic. Relative price components are centered at the
// comparable median; preferences are separate editable multipliers.
func (e Engine) Score(l domain.Listing, b Benchmarks, now time.Time) (float64, float64) {
	c := e.Config
	if c.Base == 0 {
		c = DefaultConfig()
	}
	w := c.Weights
	score := c.Base
	if l.RentMin != nil && b.MedianRent > 0 {
		score += w.RelativeRent * clamp((b.MedianRent-float64(*l.RentMin))/(b.MedianRent*.35), -1, 1)
	}
	if l.RentMin != nil && l.AreaM2 != nil && *l.AreaM2 > 0 && b.MedianPriceM2 > 0 {
		ppm := float64(*l.RentMin) / *l.AreaM2
		score += w.PriceM2 * clamp((b.MedianPriceM2-ppm)/(b.MedianPriceM2*.35), -1, 1)
	}
	score += w.District * c.District[l.District]
	score += w.Bedrooms * c.Bedroom[bedBucket(l.Bedrooms)]
	score += w.Area * c.AreaBucket[areaBucket(l.AreaM2)]
	score += w.Furnishing * c.Furnishing[l.Furnished]
	if l.NearBeach != nil && *l.NearBeach {
		v := .5
		if l.BeachDistanceM != nil {
			v = clamp(1-float64(*l.BeachDistanceM)/3000, .1, 1)
		}
		score += w.Beach * v
	}
	amenities := 0
	for _, v := range l.Amenities {
		if v {
			amenities++
		}
	}
	score += w.Amenities * math.Min(float64(amenities)/6, 1)
	if len(l.Utilities) > 0 {
		score += w.Utilities * .2
	}
	if gov, ok := l.Utilities["government_rate"].(bool); ok && gov {
		score += w.Utilities * .3
	}
	age := now.Sub(l.PublishedAt)
	if age < 0 {
		age = 0
	}
	score += w.Freshness * (2*math.Exp(-age.Hours()/(24*30)) - 1)
	confidence := ScoreConfidence(l, b.SimilarCount)
	score += w.Evidence * (2*confidence - 1)
	return round1(clamp(score, 0, 100)), math.Round(confidence*100) / 100
}

// ScoreConfidence = 0.55*weighted field coverage + 0.25*mean known-field
// extraction confidence + 0.20*log-scaled comparable sample quality.
func ScoreConfidence(l domain.Listing, sample int) float64 {
	type f struct {
		known  bool
		key    string
		weight float64
	}
	fields := []f{{l.RentMin != nil, "price", .25}, {l.District != "" && l.District != "Unknown", "district", .15}, {l.PropertyType != "", "property_type", .12}, {l.Bedrooms != nil, "bedrooms", .12}, {l.AreaM2 != nil, "area_m2", .16}, {l.Furnished != "", "furnished", .05}, {l.LocationOriginal != "" || l.Ward != "" || l.Street != "", "location_original", .05}}
	coverage, total, quality, n := 0.0, 0.0, 0.0, 0.0
	for _, x := range fields {
		total += x.weight
		if x.known {
			coverage += x.weight
			c := l.Confidence[x.key]
			if c == 0 && x.key == "location_original" {
				c = l.Confidence["district"]
			}
			quality += c
			n++
		}
	}
	if total > 0 {
		coverage /= total
	}
	if n > 0 {
		quality /= n
	}
	sampleQ := math.Min(math.Log1p(float64(sample))/math.Log1p(40), 1)
	return clamp(.55*coverage+.25*quality+.20*sampleQ, 0, 1)
}
func bedBucket(v *int) string {
	if v == nil {
		return "unknown"
	}
	if *v >= 3 {
		return "3+"
	}
	return []string{"studio", "1", "2"}[*v]
}
func areaBucket(v *float64) string {
	if v == nil {
		return "unknown"
	}
	switch {
	case *v < 25:
		return "<25"
	case *v < 35:
		return "25-35"
	case *v < 50:
		return "35-50"
	case *v < 70:
		return "50-70"
	default:
		return "70+"
	}
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
func round1(v float64) float64 { return math.Round(v*10) / 10 }
