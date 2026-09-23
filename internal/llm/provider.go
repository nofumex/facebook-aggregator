package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/egori/facebook-aggregator/internal/domain"
)

type Choice struct {
	ListingID int64  `json:"listing_id"`
	Reason    string `json:"reason"`
}
type Provider interface {
	Curate(ctx context.Context, items []domain.Listing, limit int) ([]Choice, error)
	Enrich(ctx context.Context, originalText string, rules domain.Listing) (domain.Enrichment, error)
	Check(ctx context.Context) error
	Name() string
}
type Disabled struct{}

func (Disabled) Curate(context.Context, []domain.Listing, int) ([]Choice, error) {
	return nil, fmt.Errorf("LLM disabled")
}
func (Disabled) Enrich(context.Context, string, domain.Listing) (domain.Enrichment, error) {
	return domain.Enrichment{}, fmt.Errorf("LLM disabled")
}
func (Disabled) Check(context.Context) error { return fmt.Errorf("LLM disabled") }
func (Disabled) Name() string                { return "disabled" }

type Config struct {
	Provider, BaseURL, APIKey, Model string
	Timeout                          time.Duration
	Concurrency                      int
	Temperature                      float64
	MaxTokens                        int
}
type CallMeta struct {
	Model, Provider           string
	Latency                   time.Duration
	InputTokens, OutputTokens int
}
type OpenAICompatible struct {
	cfg    Config
	client *http.Client
	sem    chan struct{}
}

func New(cfg Config) *OpenAICompatible {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-5-mini"
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	return &OpenAICompatible{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}, sem: make(chan struct{}, cfg.Concurrency)}
}
func (p *OpenAICompatible) Name() string {
	if p.cfg.Provider == "openai" {
		return "OpenAI"
	}
	return "OpenAI-compatible"
}
func (p *OpenAICompatible) Check(ctx context.Context) error {
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-p.sem }()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.cfg.BaseURL, "/")+"/models", nil)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	resp, e := p.client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, sanitize(string(b)))
	}
	return nil
}
func (p *OpenAICompatible) Curate(ctx context.Context, items []domain.Listing, limit int) ([]Choice, error) {
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-p.sem }()
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > 40 {
		items = items[:40]
	}
	type compact struct {
		ID              int64             `json:"id"`
		Rent            *int64            `json:"rent,omitempty"`
		RentMax         *int64            `json:"rent_max,omitempty"`
		Deposit         *int64            `json:"deposit,omitempty"`
		Beds            *int              `json:"beds,omitempty"`
		LeaseMonths     *int              `json:"lease_months,omitempty"`
		Area            *float64          `json:"area,omitempty"`
		District        string            `json:"district,omitempty"`
		Ward            string            `json:"ward,omitempty"`
		Address         string            `json:"address,omitempty"`
		Type            string            `json:"property_type,omitempty"`
		Furnished       string            `json:"furnished,omitempty"`
		IsRental        *bool             `json:"is_rental,omitempty"`
		NearBeach       *bool             `json:"near_beach,omitempty"`
		Foreigners      *bool             `json:"foreigners_accepted,omitempty"`
		Amenities       map[string]bool   `json:"amenities,omitempty"`
		Utilities       map[string]any    `json:"utilities,omitempty"`
		Score           float64           `json:"score"`
		ScoreConfidence float64           `json:"score_confidence"`
		FieldConfidence domain.Confidence `json:"field_confidence"`
		SourceText      string            `json:"source_text"`
	}
	in := make([]compact, 0, len(items))
	for _, x := range items {
		in = append(in, compact{ID: x.ID, Rent: x.RentMin, RentMax: x.RentMax, Deposit: x.DepositAmount, Beds: x.Bedrooms, LeaseMonths: x.LeaseMonths, Area: x.AreaM2, District: x.District, Ward: x.Ward, Address: x.Address, Type: x.PropertyType, Furnished: x.Furnished, IsRental: x.IsRental, NearBeach: x.NearBeach, Foreigners: x.ForeignersAccepted, Amenities: x.Amenities, Utilities: x.Utilities, Score: x.DealScore, ScoreConfidence: x.ScoreConfidence, FieldConfidence: x.Confidence, SourceText: truncateRunes(x.OriginalText, 900)})
	}
	data, _ := json.Marshal(in)
	prompt := fmt.Sprintf("Select at most %d genuinely exceptional value-for-money Da Nang rentals. You may return fewer or none: never fill a quota. Cross-check every structured fact against source_text, reject contradictions, ambiguous prices, missing essentials, non-rental posts and weak evidence. Rank only the strongest deals. Return ONLY JSON object {\"choices\":[{\"listing_id\":123,\"reason\":\"specific short Russian reason citing verified facts\"}]}. Never invent facts. Candidates: %s", limit, data)
	body := map[string]any{"model": p.cfg.Model, "messages": []map[string]string{{"role": "system", "content": "You are a strict rental analyst and final quality gate. Precision is more important than recall. Output strict JSON."}, {"role": "user", "content": prompt}}, "response_format": map[string]string{"type": "json_object"}, "temperature": p.cfg.Temperature}
	if p.cfg.MaxTokens > 0 {
		body["max_tokens"] = p.cfg.MaxTokens
	}
	payload, _ := json.Marshal(body)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, e := p.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return nil, e
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, sanitize(string(raw)))
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if e = json.Unmarshal(raw, &envelope); e != nil || len(envelope.Choices) == 0 {
		return nil, fmt.Errorf("invalid LLM response")
	}
	var out struct {
		Choices []Choice `json:"choices"`
	}
	if e = json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &out); e != nil {
		return nil, fmt.Errorf("invalid LLM JSON: %w", e)
	}
	allowed := map[int64]bool{}
	for _, x := range items {
		allowed[x.ID] = true
	}
	seen := map[int64]bool{}
	valid := out.Choices[:0]
	for _, x := range out.Choices {
		if allowed[x.ListingID] && !seen[x.ListingID] && strings.TrimSpace(x.Reason) != "" {
			seen[x.ListingID] = true
			x.Reason = truncateRunes(strings.TrimSpace(x.Reason), 300)
			valid = append(valid, x)
		}
	}
	return valid, nil
}

func (p *OpenAICompatible) Enrich(ctx context.Context, originalText string, rules domain.Listing) (domain.Enrichment, error) {
	out, _, err := p.EnrichDetailed(ctx, originalText, rules)
	return out, err
}

func (p *OpenAICompatible) EnrichDetailed(ctx context.Context, originalText string, rules domain.Listing) (domain.Enrichment, CallMeta, error) {
	if err := p.acquire(ctx); err != nil {
		return domain.Enrichment{}, CallMeta{}, err
	}
	defer func() { <-p.sem }()
	if strings.TrimSpace(originalText) == "" {
		return domain.Enrichment{}, CallMeta{}, fmt.Errorf("empty listing text")
	}
	ruleData, _ := json.Marshal(map[string]any{
		"facebook_post_id": rules.FacebookPostID, "published_at": rules.PublishedAt, "group_name": rules.GroupName,
		"rent_vnd": rules.RentMin, "rent_max_vnd": rules.RentMax, "bedrooms": rules.Bedrooms,
		"property_type": rules.PropertyType, "area_m2": rules.AreaM2, "district": rules.District,
		"furnished": rules.Furnished, "near_beach": rules.NearBeach, "beach_distance_m": rules.BeachDistanceM,
		"deposit_vnd": rules.DepositAmount, "utilities": rules.Utilities, "amenities": rules.Amenities,
		"foreigners_allowed": rules.ForeignersAccepted, "lease_months_min": rules.LeaseMonths,
		"confidence": rules.Confidence,
	})
	prompt := fmt.Sprintf("SOURCE:\n%s\nRULE_HINTS:%s", originalText, ruleData)
	schema := enrichmentSchema()
	responseFormat := map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "rental_enrichment", "strict": true, "schema": schema}}
	if p.cfg.Provider != "openai" {
		// Some compatible endpoints implement JSON mode but not OpenAI's
		// json_schema extension. Exact-shape validation below remains strict.
		responseFormat = map[string]any{"type": "json_object"}
	}
	body := map[string]any{
		"model": p.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "Extract structured facts from Da Nang rental ads in Vietnamese, English, Russian or mixed text. Return only schema JSON. NEVER INFER FACTS NOT SUPPORTED BY THE POST. Unknown values are null. Convert rental notation to integer VND; distinguish current rent from old price, deposit, utilities and surcharges. Preserve stated location. district must be the schema enum; use Unknown when unsupported."},
			{"role": "user", "content": prompt},
		},
		"response_format": responseFormat,
		"temperature":     0,
	}
	if p.cfg.MaxTokens > 0 {
		body["max_tokens"] = p.cfg.MaxTokens
	}
	started := time.Now()
	content, usage, err := p.chat(ctx, body)
	meta := CallMeta{Model: p.cfg.Model, Provider: p.Name(), Latency: time.Since(started), InputTokens: usage.Input, OutputTokens: usage.Output}
	if err != nil {
		return domain.Enrichment{}, meta, err
	}
	if err = validateEnrichmentShape([]byte(content)); err != nil {
		return domain.Enrichment{}, meta, err
	}
	var out domain.Enrichment
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&out); err != nil {
		return domain.Enrichment{}, meta, fmt.Errorf("invalid enrichment JSON: %w", err)
	}
	if out.IsRental == nil {
		return domain.Enrichment{}, meta, fmt.Errorf("invalid enrichment: is_rental_listing is required")
	}
	for field, confidence := range out.Confidence {
		if confidence < 0 || confidence > 1 {
			return domain.Enrichment{}, meta, fmt.Errorf("invalid confidence for %s", field)
		}
	}
	return out, meta, nil
}

// AvailableModels returns the model IDs advertised by an OpenAI-compatible endpoint.
func (p *OpenAICompatible) AvailableModels(ctx context.Context) ([]string, error) {
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-p.sem }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.cfg.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, sanitize(string(raw)))
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("invalid models response: %w", err)
	}
	ids := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if strings.TrimSpace(m.ID) != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

func validateEnrichmentShape(raw []byte) error {
	required := enrichmentKeys()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("invalid enrichment JSON: %w", err)
	}
	if len(object) != len(required) {
		return fmt.Errorf("invalid enrichment JSON: expected %d fields, got %d", len(required), len(object))
	}
	for _, key := range required {
		if _, ok := object[key]; !ok {
			return fmt.Errorf("invalid enrichment JSON: missing %s", key)
		}
	}
	return nil
}

type usage struct{ Input, Output int }

func (p *OpenAICompatible) chat(ctx context.Context, body map[string]any) (string, usage, error) {
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", usage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", usage{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", usage{}, err
	}
	if resp.StatusCode/100 != 2 {
		return "", usage{}, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, sanitize(string(raw)))
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil || len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return "", usage{}, fmt.Errorf("invalid LLM response")
	}
	return envelope.Choices[0].Message.Content, usage{envelope.Usage.PromptTokens, envelope.Usage.CompletionTokens}, nil
}

func enrichmentSchema() map[string]any {
	nullable := func(kind string) map[string]any { return map[string]any{"type": []string{kind, "null"}} }
	boolObject := func(keys []string) map[string]any {
		p := map[string]any{}
		for _, key := range keys {
			p[key] = nullable("boolean")
		}
		return map[string]any{"type": "object", "properties": p, "required": keys, "additionalProperties": false}
	}
	properties := map[string]any{
		"is_rental_listing": map[string]any{"type": "boolean"}, "rent_vnd": nullable("integer"), "rent_max_vnd": nullable("integer"),
		"bedrooms": nullable("integer"), "property_type": map[string]any{"type": []string{"string", "null"}, "enum": []any{"apartment", "house", "room", "studio", nil}},
		"rooms": nullable("integer"), "area_m2": nullable("number"),
		"district":          map[string]any{"type": "string", "enum": []string{"Son Tra", "Ngu Hanh Son", "Hai Chau", "Thanh Khe", "Lien Chieu", "Cam Le", "Hoa Vang", "Other", "Unknown"}},
		"location_original": nullable("string"), "ward": nullable("string"), "street": nullable("string"), "address": nullable("string"), "building": nullable("string"),
		"furnished":  map[string]any{"type": []string{"string", "null"}, "enum": []any{"full", "partial", "none", nil}},
		"near_beach": nullable("boolean"), "beach_distance_m": nullable("integer"), "deposit_vnd": nullable("integer"),
		"utilities": map[string]any{"type": "object", "properties": map[string]any{
			"electricity_vnd_per_kwh": nullable("integer"), "water_vnd_per_person": nullable("integer"), "water_vnd_per_month": nullable("integer"),
			"wifi_vnd_per_month": nullable("integer"), "service_vnd_per_month": nullable("integer"), "parking_vnd_per_month": nullable("integer"),
		}, "required": []string{"electricity_vnd_per_kwh", "water_vnd_per_person", "water_vnd_per_month", "wifi_vnd_per_month", "service_vnd_per_month", "parking_vnd_per_month"}, "additionalProperties": false},
		"amenities":          boolObject([]string{"balcony", "private_washing_machine", "washing_machine", "elevator", "air_conditioning", "kitchen", "pool", "gym", "parking"}),
		"restrictions":       boolObject([]string{"electric_bike_allowed"}),
		"foreigners_allowed": nullable("boolean"), "foreigner_surcharge_vnd": nullable("integer"), "pets_allowed": nullable("boolean"), "lease_months_min": nullable("integer"),
		"confidence": confidenceSchema(),
	}
	required := enrichmentKeys()
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func enrichmentKeys() []string {
	return []string{"is_rental_listing", "property_type", "bedrooms", "rooms", "area_m2", "rent_vnd", "rent_max_vnd", "deposit_vnd", "district", "location_original", "ward", "street", "address", "building", "furnished", "near_beach", "beach_distance_m", "foreigners_allowed", "foreigner_surcharge_vnd", "pets_allowed", "lease_months_min", "utilities", "amenities", "restrictions", "confidence"}
}

func confidenceSchema() map[string]any {
	keys := []string{"is_rental_listing", "property_type", "bedrooms", "rooms", "area_m2", "rent_vnd", "rent_max_vnd", "deposit_vnd", "district", "location_original", "ward", "street", "address", "building", "furnished", "near_beach", "beach_distance_m", "foreigners_allowed", "foreigner_surcharge_vnd", "pets_allowed", "lease_months_min", "utilities", "amenities", "restrictions"}
	properties := map[string]any{}
	for _, key := range keys {
		properties[key] = map[string]any{"type": []string{"number", "null"}, "minimum": 0, "maximum": 1}
	}
	return map[string]any{"type": "object", "properties": properties, "required": keys, "additionalProperties": false}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
func (p *OpenAICompatible) acquire(ctx context.Context) error {
	select {
	case p.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func sanitize(s string) string {
	if len(s) > 300 {
		s = s[:300]
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
}
