package enrichment

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egori/facebook-aggregator/internal/config"
	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/llm"
	"github.com/egori/facebook-aggregator/internal/parser"
)

const trustedRuleConfidence = .75
const minimumLLMConfidence = .60

type CacheEntry struct {
	Result                    domain.Enrichment
	Model                     string
	LatencyMS                 int
	InputTokens, OutputTokens int
}
type Cache interface {
	LoadEnrichment(context.Context, [32]byte, string) (CacheEntry, bool, error)
	SaveEnrichment(context.Context, [32]byte, string, CacheEntry) error
}

// Service performs semantic extraction once per cleaned content hash and schema version.
// It is intentionally separate from the optional final collection curator.
type Service struct {
	cfg          config.LLMExtraction
	cache        Cache
	log          *slog.Logger
	mu           sync.Mutex
	models       []string
	sem          chan struct{}
}

func New(cfg config.LLMExtraction, cache Cache, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	concurrency := cfg.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	return &Service{cfg: cfg, cache: cache, log: log, sem: make(chan struct{}, concurrency)}
}

func (s *Service) Apply(ctx context.Context, l domain.Listing) (domain.Listing, bool, error) {
	if strings.TrimSpace(l.OriginalText) == "" {
		l.ExtractionStatus = "skipped"
		return l, false, nil
	}
	if !s.cfg.Enabled || s.cfg.BaseURL == "" || s.cfg.APIKey == "" || len(s.cfg.Models) == 0 {
		l.ExtractionStatus = "pending"
		return l, false, nil
	}
	clean := parser.CleanForExtraction(l.OriginalText)
	// Cache identity follows the authoritative post content hash; only the LLM
	// input is cleaned, so any Facebook edit is eligible for re-extraction.
	hash := sha256.Sum256([]byte(l.OriginalText))
	if s.cache != nil {
		entry, ok, err := s.cache.LoadEnrichment(ctx, hash, s.cfg.SchemaVersion)
		if err != nil {
			return failed(l, s.cfg.SchemaVersion), false, err
		}
		if ok {
			l = Merge(l, entry.Result)
			success(&l, s.cfg.SchemaVersion, entry.Model)
			return l, true, nil
		}
	}
	models, discoveryErr := s.activeModels(ctx)
	if discoveryErr != nil {
		return failed(l, s.cfg.SchemaVersion), false, discoveryErr
	}
	if len(models) == 0 {
		return failed(l, s.cfg.SchemaVersion), false, fmt.Errorf("none of configured extraction models is available")
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return failed(l, s.cfg.SchemaVersion), false, ctx.Err()
	}
	var last error
	for attempt, model := range models {
		p := llm.New(llm.Config{Provider: "compatible", BaseURL: s.cfg.BaseURL, APIKey: s.cfg.APIKey, Model: model, Timeout: s.cfg.Timeout, Concurrency: s.cfg.Concurrency, MaxTokens: 1400})
		for retry := 0; retry < 2; retry++ {
			result, meta, err := p.EnrichDetailed(ctx, clean, l)
			if err == nil {
				err = ValidateAgainst(result, l)
			}
			s.log.Info("LLM extraction attempt", "facebook_post_id", l.FacebookPostID, "model", model, "attempt", attempt+1, "retry", retry, "latency_ms", meta.Latency.Milliseconds(), "valid", err == nil, "fallback_reason", errorText(err), "input_tokens", meta.InputTokens, "output_tokens", meta.OutputTokens)
			if err == nil {
				entry := CacheEntry{Result: result, Model: model, LatencyMS: int(meta.Latency.Milliseconds()), InputTokens: meta.InputTokens, OutputTokens: meta.OutputTokens}
				if s.cache != nil {
					if e := s.cache.SaveEnrichment(ctx, hash, s.cfg.SchemaVersion, entry); e != nil {
						s.log.Warn("save extraction cache", "error", e)
					}
				}
				l = Merge(l, result)
				success(&l, s.cfg.SchemaVersion, model)
				return l, true, nil
			}
			last = err
			if ctx.Err() != nil {
				return failed(l, s.cfg.SchemaVersion), false, ctx.Err()
			}
			if retry == 0 {
				select {
				case <-time.After(150 * time.Millisecond):
				case <-ctx.Done():
					return failed(l, s.cfg.SchemaVersion), false, ctx.Err()
				}
			}
		}
	}
	return failed(l, s.cfg.SchemaVersion), false, last
}

func (s *Service) activeModels(ctx context.Context) ([]string,error){
	s.mu.Lock();defer s.mu.Unlock()
	if len(s.models)>0{return append([]string(nil),s.models...),nil}
	models,err:=s.discover(ctx);if err!=nil{return nil,err};s.models=models;return append([]string(nil),models...),nil
}

func (s *Service) discover(ctx context.Context) ([]string, error) {
	p := llm.New(llm.Config{Provider: "compatible", BaseURL: s.cfg.BaseURL, APIKey: s.cfg.APIKey, Model: s.cfg.Models[0], Timeout: s.cfg.Timeout, Concurrency: 1})
	available, err := p.AvailableModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover extraction models: %w", err)
	}
	var out []string
	for _, m := range s.cfg.Models {
		if slices.Contains(available, m) {
			out = append(out, m)
		}
	}
	s.log.Info("extraction model chain discovered", "configured", s.cfg.Models, "active", out)
	return out, nil
}
func success(l *domain.Listing, version, model string) {
	now := time.Now().UTC()
	l.ExtractionStatus = "success"
	l.ExtractionVersion = version
	l.LLMModel = model
	l.LLMExtractedAt = &now
}
func failed(l domain.Listing, version string) domain.Listing {
	l.ExtractionStatus = "failed"
	l.ExtractionVersion = version
	return l
}
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var districts = map[string]bool{"Son Tra": true, "Ngu Hanh Son": true, "Hai Chau": true, "Thanh Khe": true, "Lien Chieu": true, "Cam Le": true, "Hoa Vang": true, "Other": true, "Unknown": true}

func Validate(e domain.Enrichment) error { return ValidateAgainst(e, domain.Listing{}) }
func ValidateAgainst(e domain.Enrichment, rules domain.Listing) error {
	if e.IsRental == nil {
		return fmt.Errorf("is_rental_listing is required")
	}
	if e.RentMinVND != nil && (*e.RentMinVND < 1_000_000 || *e.RentMinVND > 200_000_000) {
		return fmt.Errorf("rent_vnd outside plausible range")
	}
	if e.RentMaxVND != nil && (*e.RentMaxVND < 1_000_000 || *e.RentMaxVND > 200_000_000) {
		return fmt.Errorf("rent_max_vnd outside plausible range")
	}
	if e.RentMinVND != nil && e.RentMaxVND != nil && *e.RentMaxVND < *e.RentMinVND {
		return fmt.Errorf("rent range is reversed")
	}
	if e.Bedrooms != nil && (*e.Bedrooms < 0 || *e.Bedrooms > 20) {
		return fmt.Errorf("invalid bedrooms")
	}
	if e.Rooms != nil && (*e.Rooms < 0 || *e.Rooms > 50) {
		return fmt.Errorf("invalid rooms")
	}
	if e.AreaM2 != nil && (*e.AreaM2 < 8 || *e.AreaM2 > 1000) {
		return fmt.Errorf("invalid area_m2")
	}
	if e.District == nil || !districts[*e.District] {
		return fmt.Errorf("invalid district")
	}
	if e.Furnished != nil && *e.Furnished != "full" && *e.Furnished != "partial" && *e.Furnished != "none" {
		return fmt.Errorf("invalid furnished")
	}
	for k, c := range e.Confidence {
		if math.IsNaN(c) || c < 0 || c > 1 {
			return fmt.Errorf("invalid confidence for %s", k)
		}
	}
	if e.Confidence["is_rental_listing"] < minimumLLMConfidence {
		return fmt.Errorf("low confidence for is_rental_listing")
	}
	if e.RentMinVND != nil && e.Confidence["rent_vnd"] < minimumLLMConfidence {
		return fmt.Errorf("low confidence for rent_vnd")
	}
	if rules.RentMin != nil && rules.Confidence["price"] >= .85 && e.RentMinVND == nil {
		return fmt.Errorf("obvious rule rent omitted")
	}
	if rules.RentMin != nil && e.RentMinVND != nil && rules.Confidence["price"] >= .85 && rules.RawValues["possible_old_and_new_prices"] == nil {
		ratio := float64(*e.RentMinVND) / float64(*rules.RentMin)
		if ratio < .85 || ratio > 1.15 { return fmt.Errorf("rent contradicts high-confidence rule evidence") }
	}
	if rules.Bedrooms != nil && e.Bedrooms != nil && rules.Confidence["bedrooms"] >= .85 && *rules.Bedrooms != *e.Bedrooms {
		return fmt.Errorf("bedrooms contradict high-confidence rule evidence")
	}
	return nil
}

// Merge never replaces known data with null. Strong rule scalar values win
// conflicts except when the parser marked old/current prices as ambiguous.
func Merge(l domain.Listing, e domain.Enrichment) domain.Listing {
	if l.Confidence == nil {
		l.Confidence = domain.Confidence{}
	}
	if l.RawValues == nil {
		l.RawValues = map[string]any{}
	}
	if l.Amenities == nil {
		l.Amenities = map[string]bool{}
	}
	if l.Utilities == nil {
		l.Utilities = map[string]any{}
	}
	if l.Restrictions == nil {
		l.Restrictions = map[string]any{}
	}
	l.RawValues["extraction_sources"] = map[string]any{"rules": l.Confidence, "llm": e.Confidence}
	if e.IsRental != nil && trusted(e, "is_rental_listing") {
		l.IsRental = e.IsRental
		l.Confidence["is_rental"] = e.Confidence["is_rental_listing"]
	}
	ambiguous := l.RawValues["possible_old_and_new_prices"] != nil
	if e.RentMinVND != nil && trusted(e, "rent_vnd") && (ambiguous || l.RentMin == nil || l.Confidence["price"] < trustedRuleConfidence) {
		l.RentMin = e.RentMinVND
		l.Confidence["price"] = e.Confidence["rent_vnd"]
	}
	if e.RentMaxVND != nil && trusted(e, "rent_max_vnd") && (ambiguous || l.RentMax == nil || l.Confidence["price"] < trustedRuleConfidence) {
		l.RentMax = e.RentMaxVND
	}
	if l.RentMin != nil && l.RentMax == nil {
		v := *l.RentMin
		l.RentMax = &v
	}
	mergePtr(&l.Bedrooms, e.Bedrooms, l.Confidence, "bedrooms", e)
	mergePtr(&l.Rooms, e.Rooms, l.Confidence, "rooms", e)
	mergePtr(&l.AreaM2, e.AreaM2, l.Confidence, "area_m2", e)
	mergeString(&l.PropertyType, e.PropertyType, l.Confidence, "property_type", e)
	mergeString(&l.District, e.District, l.Confidence, "district", e)
	mergeString(&l.LocationOriginal, e.LocationOriginal, l.Confidence, "location_original", e)
	mergeString(&l.Ward, e.Ward, l.Confidence, "ward", e)
	mergeString(&l.Street, e.Street, l.Confidence, "street", e)
	mergeString(&l.Address, e.Address, l.Confidence, "address", e)
	mergeString(&l.Building, e.Building, l.Confidence, "building", e)
	mergeString(&l.Furnished, e.Furnished, l.Confidence, "furnished", e)
	mergePtr(&l.NearBeach, e.NearBeach, l.Confidence, "near_beach", e)
	mergePtr(&l.BeachDistanceM, e.BeachDistanceM, l.Confidence, "beach_distance_m", e)
	mergePtr(&l.DepositAmount, e.DepositVND, l.Confidence, "deposit_vnd", e)
	mergePtr(&l.ForeignersAccepted, e.ForeignersAccepted, l.Confidence, "foreigners_allowed", e)
	mergePtr(&l.ForeignerPrice, e.ForeignerSurcharge, l.Confidence, "foreigner_surcharge_vnd", e)
	mergePtr(&l.PetsAllowed, e.PetsAllowed, l.Confidence, "pets_allowed", e)
	mergePtr(&l.LeaseMonths, e.LeaseMonths, l.Confidence, "lease_months_min", e)
	if trusted(e, "utilities") {
		copyAny(l.Utilities, e.Utilities)
	}
	if trusted(e, "restrictions") {
		copyAny(l.Restrictions, e.Restrictions)
	}
	if trusted(e, "amenities") {
		for k, v := range e.Amenities {
			if b, ok := v.(bool); ok {
				l.Amenities[k] = b
			}
		}
	}
	l.EstimatedMonthlyTotalMin, l.EstimatedMonthlyTotalMax = estimateTotal(l)
	return l
}
func trusted(e domain.Enrichment, k string) bool { return e.Confidence[k] >= minimumLLMConfidence }
func mergePtr[T any](dst **T, src *T, c domain.Confidence, k string, e domain.Enrichment) {
	if src != nil && trusted(e, k) && (*dst == nil || c[k] < trustedRuleConfidence) {
		*dst = src
		c[k] = e.Confidence[k]
	}
}
func mergeString(dst *string, src *string, c domain.Confidence, k string, e domain.Enrichment) {
	if src != nil && strings.TrimSpace(*src) != "" && trusted(e, k) && (*dst == "" || c[k] < trustedRuleConfidence) {
		*dst = strings.TrimSpace(*src)
		c[k] = e.Confidence[k]
	}
}
func copyAny(dst, src map[string]any) {
	for k, v := range src {
		if v != nil {
			dst[k] = v
		}
	}
}
func estimateTotal(l domain.Listing) (*int64, *int64) {
	if l.RentMin == nil {
		return nil, nil
	}
	var extra int64
	for k, v := range l.Utilities {
		if !strings.HasSuffix(k, "_per_month") {
			continue
		}
		switch n := v.(type) {
		case float64:
			if n > 0 && n < 2e6 {
				extra += int64(n)
			}
		case int64:
			if n > 0 && n < 2e6 {
				extra += n
			}
		}
	}
	max := l.RentMax
	if max == nil {
		max = l.RentMin
	}
	a, b := *l.RentMin+extra, *max+extra
	return &a, &b
}
