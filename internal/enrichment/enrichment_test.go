package enrichment

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/egori/facebook-aggregator/internal/config"
	"github.com/egori/facebook-aggregator/internal/domain"
)

type cacheKey struct {
	h [32]byte
	v string
}
type memoryCache struct {
	mu    sync.Mutex
	items map[cacheKey]CacheEntry
}

func (m *memoryCache) LoadEnrichment(_ context.Context, h [32]byte, v string) (CacheEntry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[cacheKey{h, v}]
	return e, ok, nil
}
func (m *memoryCache) SaveEnrichment(_ context.Context, h [32]byte, v string, e CacheEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[cacheKey{h, v}] = e
	return nil
}

func validJSON(rent int64) string {
	b, _ := json.Marshal(map[string]any{
		"is_rental_listing": true, "property_type": "studio", "bedrooms": 0, "rooms": nil, "area_m2": nil, "rent_vnd": rent, "rent_max_vnd": nil, "deposit_vnd": nil, "district": "Ngu Hanh Son", "location_original": "Khu FPT", "ward": nil, "street": nil, "address": nil, "building": nil, "furnished": "full", "near_beach": nil, "beach_distance_m": nil, "foreigners_allowed": nil, "foreigner_surcharge_vnd": nil, "pets_allowed": nil, "lease_months_min": nil,
		"utilities": map[string]any{"electricity_vnd_per_kwh": 4000, "water_vnd_per_person": 100000, "water_vnd_per_month": nil, "wifi_vnd_per_month": nil, "service_vnd_per_month": nil, "parking_vnd_per_month": nil},
		"amenities": map[string]any{"balcony": nil, "private_washing_machine": nil, "washing_machine": true, "elevator": nil, "air_conditioning": nil, "kitchen": nil, "pool": nil, "gym": nil, "parking": nil}, "restrictions": map[string]any{"electric_bike_allowed": nil},
		"confidence": map[string]any{"is_rental_listing": .99, "property_type": .95, "bedrooms": .95, "rent_vnd": .99, "district": .9, "location_original": .9, "furnished": .9, "utilities": .9, "amenities": .8},
	})
	return string(b)
}

func extractionServer(t *testing.T, malformedFirst bool) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "light"}, map[string]any{"id": "strong"}}})
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls++
		content := validJSON(4_500_000)
		if malformedFirst && req.Model == "light" {
			content = `{`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 20}})
	}))
	return s, &calls
}

func TestCascadeFallbackAndFirstValidStops(t *testing.T) {
	for _, tc := range []struct {
		name      string
		malformed bool
		want      int
	}{{"fallback", true, 2}, {"first valid", false, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := extractionServer(t, tc.malformed)
			defer srv.Close()
			svc := New(config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"light", "strong"}}, &memoryCache{items: map[cacheKey]CacheEntry{}}, nil)
			l := domain.Listing{OriginalText: "STUDIO FULL NỘI THẤT KHU FPT – 4TR5", Confidence: domain.Confidence{}, RawValues: map[string]any{}}
			got, ok, err := svc.Apply(context.Background(), l)
			if err != nil || !ok || got.RentMin == nil || *got.RentMin != 4_500_000 {
				t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
			}
			if *calls != tc.want {
				t.Fatalf("calls=%d want=%d", *calls, tc.want)
			}
		})
	}
}

func TestTransientHTTPRetriesSameModel(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "light"}}})
					return
				}
				calls++
				if calls < 3 {
					http.Error(w, "temporary", status)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": validJSON(4_500_000)}}}})
			}))
			defer srv.Close()
			cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"light"}, RetryBase: time.Millisecond, MaxAttempts: 3}
			got, ok, err := New(cfg, nil, nil).Apply(context.Background(), domain.Listing{OriginalText: "studio 4tr5", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
			if err != nil || !ok || got.RentMin == nil || calls != 3 {
				t.Fatalf("calls=%d ok=%v err=%v got=%+v", calls, ok, err, got)
			}
		})
	}
}

func TestRateLimitImmediatelyFallsBackWithoutSameModelRetry(t *testing.T) {
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "limited"}, map[string]any{"id": "strong"}}})
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls[req.Model]++
		if req.Model == "limited" {
			w.Header().Set("Retry-After", "15")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": validJSON(4_500_000)}}}})
	}))
	defer srv.Close()
	cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"limited", "strong"}, RetryBase: time.Millisecond, MaxAttempts: 4}
	_, ok, err := New(cfg, nil, nil).Apply(context.Background(), domain.Listing{OriginalText: "studio 4tr5", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
	if err != nil || !ok || calls["limited"] != 1 || calls["strong"] != 1 {
		t.Fatalf("calls=%v ok=%v err=%v", calls, ok, err)
	}
}

func TestLastModelRateLimitDefersWithoutRapidRetries(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "limited"}}})
			return
		}
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"retryAtMs":9999999999999}}`))
	}))
	defer srv.Close()
	cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"limited"}, RetryBase: time.Millisecond, MaxAttempts: 4}
	_, ok, err := New(cfg, nil, nil).Apply(context.Background(), domain.Listing{OriginalText: "studio 4tr5", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
	if err == nil || ok || calls != 1 {
		t.Fatalf("calls=%d ok=%v err=%v", calls, ok, err)
	}
}

func TestNetworkErrorRetriesSameModel(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "light"}}})
			return
		}
		calls++
		if calls == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": validJSON(4_500_000)}}}})
	}))
	defer srv.Close()
	cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"light"}, RetryBase: time.Millisecond, MaxAttempts: 3}
	_, ok, err := New(cfg, nil, nil).Apply(context.Background(), domain.Listing{OriginalText: "studio 4tr5", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
	if err != nil || !ok || calls != 2 {
		t.Fatalf("calls=%d ok=%v err=%v", calls, ok, err)
	}
}

func TestInvalidEnumFallsBackWithoutSameModelRetry(t *testing.T) {
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "light"}, map[string]any{"id": "strong"}}})
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls[req.Model]++
		content := validJSON(4_500_000)
		if req.Model == "light" {
			var obj map[string]any
			_ = json.Unmarshal([]byte(content), &obj)
			obj["district"] = "My An"
			b, _ := json.Marshal(obj)
			content = string(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()
	cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"light", "strong"}, RetryBase: time.Millisecond, MaxAttempts: 3}
	_, ok, err := New(cfg, nil, nil).Apply(context.Background(), domain.Listing{OriginalText: "studio", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
	if err != nil || !ok || calls["light"] != 1 || calls["strong"] != 1 {
		t.Fatalf("calls=%v ok=%v err=%v", calls, ok, err)
	}
}

func TestConfiguredAutoRouterFallback(t *testing.T) {
	tests := []struct {
		name          string
		availableAuto bool
		explicitBody  string
		wantAutoCalls int
		wantSuccess   bool
	}{
		{name: "available after provider failure", availableAuto: true, wantAutoCalls: 1, wantSuccess: true},
		{name: "unavailable", availableAuto: false, wantAutoCalls: 0, wantSuccess: false},
		{name: "not used after semantic validation failure", availableAuto: true, explicitBody: `{"choices":[{"message":{"content":"{\"is_rental_listing\":true,\"district\":\"invalid\"}"}}]}`, wantAutoCalls: 0, wantSuccess: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := map[string]int{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					models := []any{map[string]any{"id": "explicit"}}
					if tc.availableAuto {
						models = append(models, map[string]any{"id": "auto:balanced"})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": models})
					return
				}
				var req struct {
					Model string `json:"model"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				calls[req.Model]++
				if req.Model == "auto:balanced" {
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": validJSON(4_500_000)}}}})
					return
				}
				if tc.explicitBody != "" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.explicitBody))
					return
				}
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"explicit"}, AutoModel: "auto:balanced", RetryBase: time.Millisecond, MaxAttempts: 1}
			_, ok, err := New(cfg, nil, nil).Apply(context.Background(), domain.Listing{OriginalText: "studio 4tr5", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
			if ok != tc.wantSuccess || (err == nil) != tc.wantSuccess || calls["auto:balanced"] != tc.wantAutoCalls {
				t.Fatalf("calls=%v ok=%v err=%v", calls, ok, err)
			}
		})
	}
}

func TestAutoModelInExplicitListRunsAsOrdinaryModel(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "auto:balanced"}}})
			return
		}
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": validJSON(4_500_000)}}}})
	}))
	defer srv.Close()
	cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"auto:balanced"}, AutoModel: "auto:balanced"}
	got, ok, err := New(cfg, nil, nil).Apply(context.Background(), domain.Listing{OriginalText: "studio 4tr5", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
	if err != nil || !ok || calls != 1 || got.LLMModel != "auto:balanced" {
		t.Fatalf("calls=%d model=%q ok=%v err=%v", calls, got.LLMModel, ok, err)
	}
}

func TestAutoRouterIsCalledAtMostOnce(t *testing.T) {
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "explicit"}, map[string]any{"id": "auto:balanced"}}})
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls[req.Model]++
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"explicit"}, AutoModel: "auto:balanced", RetryBase: time.Millisecond, MaxAttempts: 4}
	_, _, _ = New(cfg, nil, nil).Apply(context.Background(), domain.Listing{OriginalText: "studio 4tr5", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
	if calls["explicit"] != 4 || calls["auto:balanced"] != 1 {
		t.Fatalf("calls=%v", calls)
	}
}

func TestSharedModelCooldownSkipsConcurrentWorker(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	var once sync.Once
	calls := 0
	var callsMu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "limited"}}})
			return
		}
		callsMu.Lock()
		calls++
		callsMu.Unlock()
		once.Do(func() { close(requestStarted) })
		<-releaseResponse
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()
	cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"limited"}, RetryBase: time.Millisecond, MaxAttempts: 4}
	svc := New(cfg, nil, nil)
	done := make(chan struct{}, 2)
	go func() {
		_, _, _ = svc.Apply(context.Background(), domain.Listing{OriginalText: "listing one", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
		done <- struct{}{}
	}()
	<-requestStarted
	go func() {
		_, _, _ = svc.Apply(context.Background(), domain.Listing{OriginalText: "listing two", Confidence: domain.Confidence{}, RawValues: map[string]any{}})
		done <- struct{}{}
	}()
	close(releaseResponse)
	<-done
	<-done
	callsMu.Lock()
	defer callsMu.Unlock()
	if calls != 1 {
		t.Fatalf("rate-limited model called %d times", calls)
	}
}

func TestCacheUsesContentHashAndSchemaVersion(t *testing.T) {
	srv, calls := extractionServer(t, false)
	defer srv.Close()
	cache := &memoryCache{items: map[cacheKey]CacheEntry{}}
	cfg := config.LLMExtraction{Enabled: true, BaseURL: srv.URL, APIKey: "x", Timeout: time.Second, Concurrency: 1, SchemaVersion: "v1", Models: []string{"light"}}
	l := domain.Listing{OriginalText: "studio 4tr5", Confidence: domain.Confidence{}, RawValues: map[string]any{}}
	svc := New(cfg, cache, nil)
	_, _, _ = svc.Apply(context.Background(), l)
	_, _, _ = svc.Apply(context.Background(), l)
	l.OriginalText += " edited"
	_, _, _ = svc.Apply(context.Background(), l)
	cfg.SchemaVersion = "v2"
	_, _, _ = New(cfg, cache, nil).Apply(context.Background(), l)
	if *calls != 3 {
		t.Fatalf("calls=%d", *calls)
	}
	_ = sha256.Sum256(nil)
}

func TestUnavailableKeepsRulesPendingOrFailed(t *testing.T) {
	rent := int64(5_000_000)
	l := domain.Listing{OriginalText: "rent 5m", RentMin: &rent, Confidence: domain.Confidence{"price": .9}}
	svc := New(config.LLMExtraction{Enabled: true, BaseURL: "http://127.0.0.1:1", APIKey: "x", Timeout: 50 * time.Millisecond, SchemaVersion: "v1", Models: []string{"light"}}, nil, nil)
	got, ok, err := svc.Apply(context.Background(), l)
	if err == nil || ok || got.RentMin == nil || *got.RentMin != rent || got.ExtractionStatus != "failed" {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestMergeOldPriceUtilitiesAndNoHallucination(t *testing.T) {
	old, current, deposit := int64(7_000_000), int64(5_000_000), int64(5_000_000)
	yes := true
	district := "Son Tra"
	l := domain.Listing{RentMin: &old, Confidence: domain.Confidence{"price": .9}, RawValues: map[string]any{"possible_old_and_new_prices": true}}
	e := domain.Enrichment{IsRental: &yes, RentMinVND: &current, DepositVND: &deposit, District: &district, Utilities: map[string]any{"electricity_vnd_per_kwh": float64(4000)}, Confidence: domain.Confidence{"rent_vnd": .99, "deposit_vnd": .9, "district": .9, "utilities": .9}}
	got := Merge(l, e)
	if *got.RentMin != current || *got.DepositAmount != deposit || got.AreaM2 != nil || got.Utilities["electricity_vnd_per_kwh"] != float64(4000) {
		t.Fatalf("%+v", got)
	}
}

func TestSurchargeAndUtilitiesNeverBecomeRent(t *testing.T) {
	rent, surcharge := int64(4_500_000), int64(300_000)
	yes := true
	district := "Ngu Hanh Son"
	e := domain.Enrichment{IsRental: &yes, RentMinVND: &rent, District: &district, ForeignerSurcharge: &surcharge, Utilities: map[string]any{"electricity_vnd_per_kwh": float64(4000), "water_vnd_per_person": float64(100000)}, Confidence: domain.Confidence{"rent_vnd": .99, "district": .9, "foreigner_surcharge_vnd": .95, "utilities": .95}}
	got := Merge(domain.Listing{Confidence: domain.Confidence{}, RawValues: map[string]any{}}, e)
	if got.RentMin == nil || *got.RentMin != rent || got.ForeignerPrice == nil || *got.ForeignerPrice != surcharge {
		t.Fatalf("%+v", got)
	}
	if got.Utilities["electricity_vnd_per_kwh"] != float64(4000) || got.Utilities["water_vnd_per_person"] != float64(100000) {
		t.Fatalf("%+v", got.Utilities)
	}
}

func TestValidatorRejectsNonCanonicalDistrict(t *testing.T) {
	yes := true
	district := "My An"
	err := Validate(domain.Enrichment{IsRental: &yes, District: &district, Confidence: domain.Confidence{"is_rental_listing": .9}})
	if err == nil {
		t.Fatal("non-canonical district accepted")
	}
}

func TestModelSpecificTimeout(t *testing.T) {
	svc := New(config.LLMExtraction{Timeout: 20 * time.Second, ModelTimeouts: map[string]time.Duration{"strong": 45 * time.Second}}, nil, nil)
	if got := svc.modelTimeout("light", 0, 3); got != 20*time.Second {
		t.Fatalf("light=%s", got)
	}
	if got := svc.modelTimeout("strong", 1, 3); got != 45*time.Second {
		t.Fatalf("strong=%s", got)
	}
	if got := svc.modelTimeout("final", 2, 3); got != 60*time.Second {
		t.Fatalf("final=%s", got)
	}
}
