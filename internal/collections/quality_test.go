package collections

import (
	"testing"

	"github.com/egori/facebook-aggregator/internal/domain"
)

func TestEligibilityRequiresReliableParsedData(t *testing.T) {
	rent := int64(8_000_000)
	beds := 2
	good := domain.Listing{RentMin: &rent, Bedrooms: &beds, District: "Sơn Trà", DealScore: 78, ScoreConfidence: .72, Confidence: domain.Confidence{"price": .9}}
	if !IsEligible(good) {
		t.Fatal("complete high-scoring listing must be eligible")
	}

	tests := map[string]domain.Listing{
		"no price":             {Bedrooms: &beds, District: "Sơn Trà", DealScore: 78, ScoreConfidence: .72, Confidence: domain.Confidence{"price": .9}},
		"uncertain price":      {RentMin: &rent, Bedrooms: &beds, District: "Sơn Trà", DealScore: 78, ScoreConfidence: .72, Confidence: domain.Confidence{"price": .6}},
		"weak evidence":        {RentMin: &rent, Bedrooms: &beds, District: "Sơn Trà", DealScore: 78, ScoreConfidence: .4, Confidence: domain.Confidence{"price": .9}},
		"not a top deal":       {RentMin: &rent, Bedrooms: &beds, District: "Sơn Trà", DealScore: 58, ScoreConfidence: .72, Confidence: domain.Confidence{"price": .9}},
		"too few parsed facts": {RentMin: &rent, District: "Sơn Trà", DealScore: 78, ScoreConfidence: .72, Confidence: domain.Confidence{"price": .9}},
	}
	for name, listing := range tests {
		t.Run(name, func(t *testing.T) {
			if IsEligible(listing) {
				t.Fatal("incomplete listing entered collection")
			}
		})
	}
}
