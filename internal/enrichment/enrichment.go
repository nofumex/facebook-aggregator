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
	cfg        config.LLMExtraction
	cache      Cache
	log        *slog.Logger
	mu         sync.Mutex
	discovered bool
	models     []string
	autoModel  string
	cooldown   map[string]time.Time
	sem        chan struct{}
}

func New(cfg config.LLMExtraction, cache Cache, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	concurrency := cfg.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	return &Service{cfg: cfg, cache: cache, log: log, cooldown: map[string]time.Time{}, sem: make(chan struct{}, concurrency)}
}

func (s *Service) Apply(ctx context.Context, l domain.Listing) (domain.Listing, bool, error) {
	if strings.TrimSpace(l.OriginalText) == "" {
		l.ExtractionStatus = "skipped"
		return l, false, nil
	}
	if !s.cfg.Enabled || s.cfg.BaseURL == "" || s.cfg.APIKey == "" || len(s.cfg.Models) == 0 && s.cfg.AutoModel == "" {
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
	models, autoModel, discoveryErr := s.activeModels(ctx)
	if discoveryErr != nil {
		return failed(l, s.cfg.SchemaVersion), false, discoveryErr
	}
	if len(models) == 0 && autoModel == "" {
		return failed(l, s.cfg.SchemaVersion), false, fmt.Errorf("none of configured extraction models is available")
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return failed(l, s.cfg.SchemaVersion), false, ctx.Err()
	}
	var last error
	operationalOnly := true
	for modelIndex, model := range models {
		if until, cooling := s.modelCoolingDown(model); cooling {
			last = fmt.Errorf("model %s is cooling down until %s", model, until.Format(time.RFC3339))
			action := "defer"
			if modelIndex+1 < len(models) || autoModel != "" && operationalOnly {
				action = "fallback"
			}
			s.log.Info("LLM extraction model skipped", "facebook_post_id", l.FacebookPostID, "model", model, "error_class", "rate_limit", "retry_at", until.Format(time.RFC3339), "action", action)
			continue
		}
		p := llm.New(llm.Config{Provider: "compatible", BaseURL: s.cfg.BaseURL, APIKey: s.cfg.APIKey, Model: model, Timeout: s.modelTimeout(model, modelIndex, len(models)), Concurrency: s.cfg.Concurrency, MaxTokens: 1400})
		attempts := s.cfg.MaxAttempts
		if attempts < 1 {
			attempts = 4
		}
		for retry := 0; retry < attempts; retry++ {
			result, meta, err := p.EnrichDetailed(ctx, clean, l)
			if err == nil {
				err = ValidateAgainst(result, l)
			}
			s.logResponseShape(l.FacebookPostID, model, err, meta.ResponseKeys)
			s.log.Info("LLM extraction attempt", "facebook_post_id", l.FacebookPostID, "model", model, "model_index", modelIndex+1, "attempt", retry+1, "latency_ms", meta.Latency.Milliseconds(), "valid", err == nil, "fallback_reason", errorText(err), "input_tokens", meta.InputTokens, "output_tokens", meta.OutputTokens)
			if err == nil {
				return s.saveSuccess(ctx, l, hash, result, meta, model)
			}
			last = err
			if ctx.Err() != nil {
				return failed(l, s.cfg.SchemaVersion), false, ctx.Err()
			}
			if llm.IsRateLimit(err) {
				until, retryAfter := s.setModelCooldown(model, err)
				action := "defer"
				if modelIndex+1 < len(models) || autoModel != "" && operationalOnly {
					action = "fallback"
				}
				s.log.Warn("LLM extraction rate limited", "facebook_post_id", l.FacebookPostID, "model", model, "error_class", "rate_limit", "retry_after_ms", retryAfter.Milliseconds(), "retry_at", until.Format(time.RFC3339), "action", action)
				break
			}
			if !llm.IsTransient(err) || retry+1 >= attempts {
				if !llm.IsTransient(err) {
					operationalOnly = false
				}
				break
			}
			base := s.cfg.RetryBase
			if base <= 0 {
				base = 500 * time.Millisecond
			}
			wait := base * time.Duration(1<<retry)
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return failed(l, s.cfg.SchemaVersion), false, ctx.Err()
			}
		}
	}
	if autoModel != "" && operationalOnly {
		if until, cooling := s.modelCoolingDown(autoModel); cooling {
			last = fmt.Errorf("auto model %s is cooling down until %s", autoModel, until.Format(time.RFC3339))
			s.log.Info("LLM auto router skipped", "facebook_post_id", l.FacebookPostID, "model", autoModel, "error_class", "rate_limit", "retry_at", until.Format(time.RFC3339), "action", "defer")
		} else {
			p := llm.New(llm.Config{Provider: "compatible", BaseURL: s.cfg.BaseURL, APIKey: s.cfg.APIKey, Model: autoModel, Timeout: s.modelTimeout(autoModel, len(models), len(models)+1), Concurrency: s.cfg.Concurrency, MaxTokens: 1400})
			result, meta, err := p.EnrichDetailed(ctx, clean, l)
			if err == nil {
				err = ValidateAgainst(result, l)
			}
			s.logResponseShape(l.FacebookPostID, autoModel, err, meta.ResponseKeys)
			s.log.Info("LLM extraction auto router attempt", "facebook_post_id", l.FacebookPostID, "model", autoModel, "attempt", 1, "latency_ms", meta.Latency.Milliseconds(), "valid", err == nil, "fallback_reason", errorText(err), "input_tokens", meta.InputTokens, "output_tokens", meta.OutputTokens)
			if err == nil {
				return s.saveSuccess(ctx, l, hash, result, meta, autoModel)
			}
			last = err
			if llm.IsRateLimit(err) {
				until, retryAfter := s.setModelCooldown(autoModel, err)
				s.log.Warn("LLM extraction rate limited", "facebook_post_id", l.FacebookPostID, "model", autoModel, "error_class", "rate_limit", "retry_after_ms", retryAfter.Milliseconds(), "retry_at", until.Format(time.RFC3339), "action", "defer")
			}
		}
	}
	return failed(l, s.cfg.SchemaVersion), false, last
}

func (s *Service) logResponseShape(postID, model string, err error, keys []string) {
	if err != nil && strings.Contains(err.Error(), "missing is_rental_listing") {
		s.log.Debug("LLM response missing rental flag", "facebook_post_id", postID, "model", model, "response_keys", keys)
	}
}

func (s *Service) saveSuccess(ctx context.Context, l domain.Listing, hash [32]byte, result domain.Enrichment, meta llm.CallMeta, model string) (domain.Listing, bool, error) {
	entry := CacheEntry{Result: result, Model: model, LatencyMS: int(meta.Latency.Milliseconds()), InputTokens: meta.InputTokens, OutputTokens: meta.OutputTokens}
	if s.cache != nil {
		if err := s.cache.SaveEnrichment(ctx, hash, s.cfg.SchemaVersion, entry); err != nil {
			s.log.Warn("save extraction cache", "error", err)
		}
	}
	l = Merge(l, result)
	success(&l, s.cfg.SchemaVersion, model)
	return l, true, nil
}

func (s *Service) modelTimeout(model string, index, total int) time.Duration {
	if d := s.cfg.ModelTimeouts[model]; d > 0 {
		return d
	}
	base := s.cfg.Timeout
	if base <= 0 {
		base = 25 * time.Second
	}
	if index >= total-2 && base < 60*time.Second {
		return 60 * time.Second
	}
	return base
}

func (s *Service) activeModels(ctx context.Context) ([]string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.discovered {
		return append([]string(nil), s.models...), s.autoModel, nil
	}
	models, autoModel, err := s.discover(ctx)
	if err != nil {
		return nil, "", err
	}
	s.discovered = true
	s.models = models
	s.autoModel = autoModel
	return append([]string(nil), models...), autoModel, nil
}

func (s *Service) discover(ctx context.Context) ([]string, string, error) {
	seedModel := s.cfg.AutoModel
	if len(s.cfg.Models) > 0 {
		seedModel = s.cfg.Models[0]
	}
	p := llm.New(llm.Config{Provider: "compatible", BaseURL: s.cfg.BaseURL, APIKey: s.cfg.APIKey, Model: seedModel, Timeout: s.cfg.Timeout, Concurrency: 1})
	available, err := p.AvailableModels(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("discover extraction models: %w", err)
	}
	var out []string
	for _, m := range s.cfg.Models {
		if slices.Contains(available, m) {
			out = append(out, m)
		}
	}
	autoModel := ""
	if s.cfg.AutoModel != "" && slices.Contains(available, s.cfg.AutoModel) && !slices.Contains(out, s.cfg.AutoModel) {
		autoModel = s.cfg.AutoModel
	}
	s.log.Info("extraction model chain discovered", "configured", s.cfg.Models, "active", out, "configured_auto_model", s.cfg.AutoModel, "active_auto_model", autoModel)
	return out, autoModel, nil
}

func (s *Service) modelCoolingDown(model string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.cooldown[model]
	if !ok {
		return time.Time{}, false
	}
	if !until.After(time.Now()) {
		delete(s.cooldown, model)
		return time.Time{}, false
	}
	return until, true
}

func (s *Service) setModelCooldown(model string, err error) (time.Time, time.Duration) {
	now := time.Now()
	at, retryAfter, known := llm.RateLimitReset(err)
	if at.IsZero() && retryAfter > 0 {
		at = now.Add(retryAfter)
	}
	if !known || !at.After(now) {
		retryAfter = 45 * time.Second
		at = now.Add(retryAfter)
	} else {
		retryAfter = at.Sub(now)
	}
	s.mu.Lock()
	if current := s.cooldown[model]; current.After(at) {
		at = current
		retryAfter = at.Sub(now)
	} else {
		s.cooldown[model] = at
	}
	s.mu.Unlock()
	return at, retryAfter
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
	if e.PropertyType != nil && *e.PropertyType != "apartment" && *e.PropertyType != "house" && *e.PropertyType != "room" && *e.PropertyType != "studio" {
		return fmt.Errorf("invalid property_type")
	}
	if e.District == nil || !domain.IsCanonicalDistrict(*e.District) {
		return fmt.Errorf("invalid district")
	}
	if e.Furnished != nil && *e.Furnished != "full" && *e.Furnished != "partial" && *e.Furnished != "none" {
		return fmt.Errorf("invalid furnished")
	}
	for key, value := range e.Utilities {
		if value == nil {
			continue
		}
		n, ok := value.(float64)
		if !ok || n < 0 || n > 200_000_000 {
			return fmt.Errorf("invalid utility %s", key)
		}
	}
	for key, value := range e.Amenities {
		if value == nil {
			continue
		}
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("invalid amenity %s", key)
		}
	}
	for key, value := range e.Restrictions {
		if value == nil {
			continue
		}
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("invalid restriction %s", key)
		}
	}
	for k, c := range e.Confidence {
		if math.IsNaN(c) || c < 0 || c > 1 {
			return fmt.Errorf("invalid confidence for %s", k)
		}
	}
	if c := e.Confidence["is_rental_listing"]; c > 0 && c < minimumLLMConfidence {
		return fmt.Errorf("low confidence for is_rental_listing")
	}
	if c := e.Confidence["rent_vnd"]; e.RentMinVND != nil && c > 0 && c < minimumLLMConfidence {
		return fmt.Errorf("low confidence for rent_vnd")
	}
	if rules.RentMin != nil && rules.Confidence["price"] >= .85 && e.RentMinVND == nil {
		return fmt.Errorf("obvious rule rent omitted")
	}
	if rules.RentMin != nil && e.RentMinVND != nil && rules.Confidence["price"] >= .85 && rules.RawValues["possible_old_and_new_prices"] == nil {
		ratio := float64(*e.RentMinVND) / float64(*rules.RentMin)
		if ratio < .85 || ratio > 1.15 {
			return fmt.Errorf("rent contradicts high-confidence rule evidence")
		}
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
	l.RawValues["llm_enriched"] = true
	if e.IsRental != nil && (l.IsRental == nil || trusted(e, "is_rental_listing")) {
		l.IsRental = e.IsRental
		l.Confidence["is_rental"] = e.Confidence["is_rental_listing"]
	}
	ambiguous := l.RawValues["possible_old_and_new_prices"] != nil
	if e.RentMinVND != nil && (l.RentMin == nil || trusted(e, "rent_vnd") && (ambiguous || l.Confidence["price"] < trustedRuleConfidence)) {
		l.RentMin = e.RentMinVND
		l.Confidence["price"] = e.Confidence["rent_vnd"]
	}
	if e.RentMaxVND != nil && (l.RentMax == nil || trusted(e, "rent_max_vnd") && (ambiguous || l.Confidence["price"] < trustedRuleConfidence)) {
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
	if l.District == "" && e.District != nil && *e.District == domain.DistrictUnknown {
		l.District = domain.DistrictUnknown
		l.Confidence["district"] = 0
	}
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
	if usable(e, "utilities") {
		copyAny(l.Utilities, e.Utilities)
	}
	if usable(e, "restrictions") {
		copyAny(l.Restrictions, e.Restrictions)
	}
	if usable(e, "amenities") {
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
func usable(e domain.Enrichment, k string) bool  { return e.Confidence[k] == 0 || trusted(e, k) }
func mergePtr[T any](dst **T, src *T, c domain.Confidence, k string, e domain.Enrichment) {
	if src != nil && (*dst == nil || trusted(e, k) && c[k] < trustedRuleConfidence) {
		*dst = src
		c[k] = e.Confidence[k]
	}
}
func mergeString(dst *string, src *string, c domain.Confidence, k string, e domain.Enrichment) {
	if src != nil && strings.TrimSpace(*src) != "" && (*dst == "" || trusted(e, k) && c[k] < trustedRuleConfidence) {
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
