package collections

import (
	"fmt"
	"strings"

	"github.com/egori/facebook-aggregator/internal/domain"
)

const (
	MinimumDealScore       = 62.0
	MinimumScoreConfidence = 0.65
	MinimumPriceConfidence = 0.75
)

// Eligible is the single admission gate shared by local and LLM curation.
// It intentionally prefers a short, trustworthy collection to filling a quota.
func Eligible(items []domain.Listing) []domain.Listing {
	out := make([]domain.Listing, 0, len(items))
	for _, l := range items {
		if IsEligible(l) {
			out = append(out, l)
		}
	}
	return out
}

func IsEligible(l domain.Listing) bool {
	if l.RentMin == nil || *l.RentMin < 1_000_000 || *l.RentMin > 200_000_000 {
		return false
	}
	if l.Confidence["price"] < MinimumPriceConfidence || l.ScoreConfidence < MinimumScoreConfidence || l.DealScore < MinimumDealScore {
		return false
	}
	facts := 0
	for _, present := range []bool{l.Bedrooms != nil, l.AreaM2 != nil, l.District != "", l.PropertyType != ""} {
		if present {
			facts++
		}
	}
	if facts < 2 {
		return false
	}
	// A wildly broad range is normally several unrelated amounts misclassified
	// as rent (old price, deposit or fees), not a reliable asking price.
	if l.RentMax != nil && *l.RentMax > *l.RentMin*2 {
		return false
	}
	return true
}

func localReason(l domain.Listing) string {
	parts := make([]string, 0, 3)
	if l.District != "" {
		parts = append(parts, l.District)
	}
	if l.AreaM2 != nil {
		parts = append(parts, fmt.Sprintf("%.0f м²", *l.AreaM2))
	}
	if l.Bedrooms != nil {
		if *l.Bedrooms == 0 {
			parts = append(parts, "студия")
		} else {
			parts = append(parts, fmt.Sprintf("%d сп.", *l.Bedrooms))
		}
	}
	if len(parts) > 0 {
		return fmt.Sprintf("Сильное соотношение цены и характеристик: %s; рейтинг %.0f/100.", strings.Join(parts, ", "), l.DealScore)
	}
	return fmt.Sprintf("Сильное соотношение цены и характеристик; рейтинг %.0f/100.", l.DealScore)
}
