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
	Check(ctx context.Context) error
	Name() string
}
type Disabled struct{}

func (Disabled) Curate(context.Context, []domain.Listing, int) ([]Choice, error) {
	return nil, fmt.Errorf("LLM disabled")
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
		ID                        int64    `json:"id"`
		Rent                      *int64   `json:"rent"`
		RentMax                   *int64   `json:"rent_max,omitempty"`
		Beds                      *int     `json:"beds"`
		Area                      *float64 `json:"area"`
		District, Type, Furnished string
		NearBeach, Foreigners     *bool
		Amenities                 map[string]bool
		Score                     float64
		ScoreConfidence           float64           `json:"score_confidence"`
		FieldConfidence           domain.Confidence `json:"field_confidence"`
		SourceText                string            `json:"source_text"`
	}
	in := make([]compact, 0, len(items))
	for _, x := range items {
		in = append(in, compact{ID: x.ID, Rent: x.RentMin, RentMax: x.RentMax, Beds: x.Bedrooms, Area: x.AreaM2, District: x.District, Type: x.PropertyType, Furnished: x.Furnished, NearBeach: x.NearBeach, Foreigners: x.ForeignersAccepted, Amenities: x.Amenities, Score: x.DealScore, ScoreConfidence: x.ScoreConfidence, FieldConfidence: x.Confidence, SourceText: truncateRunes(x.OriginalText, 900)})
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
			x.Reason = strings.TrimSpace(x.Reason)
			valid = append(valid, x)
		}
	}
	return valid, nil
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
