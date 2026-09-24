package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egori/facebook-aggregator/internal/domain"
)

func TestEnrichMixedVietnameseEnglishRussianPost(t *testing.T) {
	var requestBody string
	result := map[string]any{"is_rental_listing": true, "property_type": "apartment", "bedrooms": 2, "rooms": nil, "area_m2": 60, "rent_vnd": 7_500_000, "rent_max_vnd": 7_500_000, "deposit_vnd": 7_500_000, "district": "Son Tra", "location_original": "An Hải Bắc", "ward": "An Hải Bắc", "street": nil, "address": nil, "building": nil, "furnished": "full", "near_beach": true, "beach_distance_m": 500, "foreigners_allowed": true, "foreigner_surcharge_vnd": nil, "pets_allowed": nil, "lease_months_min": 12,
		"utilities": map[string]any{"electricity_vnd_per_kwh": 4000, "water_vnd_per_person": nil, "water_vnd_per_month": nil, "wifi_vnd_per_month": nil, "service_vnd_per_month": nil, "parking_vnd_per_month": nil}, "amenities": map[string]any{"balcony": true, "private_washing_machine": true, "washing_machine": true, "elevator": nil, "air_conditioning": nil, "kitchen": nil, "pool": nil, "gym": nil, "parking": nil}, "restrictions": map[string]any{"electric_bike_allowed": nil}, "confidence": map[string]any{"is_rental_listing": .99, "rent_vnd": .98, "rent_max_vnd": .98, "bedrooms": .96, "property_type": .94, "area_m2": .95, "district": .95, "location_original": .9, "ward": .9, "furnished": .9, "near_beach": .9, "beach_distance_m": .95, "deposit_vnd": .96, "utilities": .9, "amenities": .9, "foreigners_allowed": .88, "lease_months_min": .9}}
	content, _ := json.Marshal(result)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		requestBody = string(raw)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}})
	}))
	defer srv.Close()
	p := New(Config{Provider: "openai", BaseURL: srv.URL, APIKey: "test", Model: "test", Concurrency: 1})
	source := "Cho thuê căn hộ 2PN 60m², full nội thất, 500m đến biển. Rent 7.5tr/month, залог 1 месяц. Foreigners welcome."
	got, err := p.Enrich(context.Background(), source, domain.Listing{Confidence: domain.Confidence{}})
	if err != nil {
		t.Fatal(err)
	}
	if got.RentMinVND == nil || *got.RentMinVND != 7_500_000 || got.Bedrooms == nil || *got.Bedrooms != 2 || got.Ward == nil || *got.Ward != "An Hải Bắc" {
		t.Fatalf("%+v", got)
	}
	for _, required := range []string{source, "NEVER INFER FACTS", "old price", "deposit", "Russian", `"strict":true`} {
		if !strings.Contains(requestBody, required) {
			t.Fatalf("missing %q", required)
		}
	}
}

func TestCompatiblePartialObjectNormalizesCanonicalSchema(t *testing.T) {
	content := `{"is_rental_listing":true,"property_type":"studio","bedrooms":0,"rent_vnd":4500000,"furnished":"full","utilities":{"electricity_vnd_per_kwh":4000},"amenities":{"washing_machine":true},"confidence":{"rent_vnd":0.9}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()
	got, err := New(Config{Provider: "compatible", BaseURL: srv.URL, APIKey: "x", Model: "m"}).Enrich(context.Background(), "STUDIO KHU FPT 4TR5", domain.Listing{})
	if err != nil {
		t.Fatal(err)
	}
	if got.District == nil || *got.District != "Unknown" || got.AreaM2 != nil || got.Utilities["water_vnd_per_person"] != nil || got.Amenities["balcony"] != nil || got.Restrictions["electric_bike_allowed"] != nil || got.Confidence["area_m2"] != 0 {
		t.Fatalf("not canonical: %+v", got)
	}
}

func TestCompatibleRequestContainsCanonicalTemplate(t *testing.T) {
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		ResponseFormat map[string]any `json:"response_format"`
	}
	response, _ := json.Marshal(canonicalEnrichmentTemplate())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(response)}}}})
	}))
	defer srv.Close()
	got, err := New(Config{Provider: "compatible", BaseURL: srv.URL, APIKey: "x", Model: "auto"}).Enrich(context.Background(), "rental source", domain.Listing{})
	if err != nil {
		t.Fatal(err)
	}
	if got.IsRental == nil || *got.IsRental || request.ResponseFormat["type"] != "json_object" {
		t.Fatalf("got=%+v response_format=%v", got, request.ResponseFormat)
	}
	if len(request.Messages) == 0 {
		t.Fatal("missing messages")
	}
	systemPrompt := request.Messages[0].Content
	for _, phrase := range []string{"ALWAYS include \"is_rental_listing\"", "MUST be boolean true or false", "must never be omitted", "never invent extra keys", "Use exactly the field names"} {
		if !strings.Contains(systemPrompt, phrase) {
			t.Fatalf("compatible prompt missing %q", phrase)
		}
	}
	for _, key := range enrichmentKeys() {
		if !strings.Contains(systemPrompt, `"`+key+`"`) {
			t.Fatalf("compatible prompt missing canonical key %q", key)
		}
	}
	canonicalTemplate := canonicalEnrichmentTemplate()
	for _, group := range []string{"utilities", "amenities", "restrictions", "confidence"} {
		for key := range canonicalTemplate[group].(map[string]any) {
			if !strings.Contains(systemPrompt, `"`+key+`"`) {
				t.Fatalf("compatible prompt missing %s canonical key %q", group, key)
			}
		}
	}
}

func TestMissingRentalFlagReturnsOnlyResponseKeyMetadata(t *testing.T) {
	content := `{"rent_vnd":6500000,"district":"Son Tra","group_name":"metadata"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()
	_, meta, err := New(Config{Provider: "compatible", BaseURL: srv.URL, APIKey: "x", Model: "auto"}).EnrichDetailed(context.Background(), "secret post text", domain.Listing{})
	if err == nil || strings.Join(meta.ResponseKeys, ",") != "district,group_name,rent_vnd" {
		t.Fatalf("keys=%v err=%v", meta.ResponseKeys, err)
	}
	for _, key := range meta.ResponseKeys {
		if strings.Contains(key, "secret") {
			t.Fatalf("raw source leaked through response keys: %v", meta.ResponseKeys)
		}
	}
}

func TestOpenAIRequestStillUsesStrictJSONSchema(t *testing.T) {
	var responseFormat map[string]any
	content, _ := json.Marshal(canonicalEnrichmentTemplate())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ResponseFormat map[string]any `json:"response_format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		responseFormat = request.ResponseFormat
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}})
	}))
	defer srv.Close()
	_, err := New(Config{Provider: "openai", BaseURL: srv.URL, APIKey: "x", Model: "strict"}).Enrich(context.Background(), "rental source", domain.Listing{})
	if err != nil {
		t.Fatal(err)
	}
	if responseFormat["type"] != "json_schema" {
		t.Fatalf("response_format=%v", responseFormat)
	}
	schemaConfig, ok := responseFormat["json_schema"].(map[string]any)
	if !ok || schemaConfig["strict"] != true {
		t.Fatalf("strict json_schema missing: %v", responseFormat)
	}
}

func TestCompatibleDropsUnknownTopLevelAndNestedFields(t *testing.T) {
	content := `{
		"is_rental_listing":true,
		"rent_vnd":6500000,
		"district":"Son Tra",
		"group_name":"Da Nang rentals",
		"property":{"kind":"apartment"},
		"priceUnitText":"6.5 million/month",
		"facebook_post_id":"post-123",
		"published_at":"2026-09-23T10:00:00Z",
		"contact":{"phone":"hidden"},
		"water_vnd":100000,
		"utilities":{"electricity_vnd_per_kwh":4000,"water_vnd":100000},
		"amenities":{"balcony":true,"sea_view":true},
		"restrictions":{"electric_bike_allowed":false,"smoking_allowed":true},
		"confidence":{"rent_vnd":0.95,"group_name":0.9}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()

	got, err := New(Config{Provider: "compatible", BaseURL: srv.URL, APIKey: "x", Model: "m"}).Enrich(context.Background(), "Apartment Son Tra 6.5m", domain.Listing{})
	if err != nil {
		t.Fatal(err)
	}
	if got.RentMinVND == nil || *got.RentMinVND != 6_500_000 {
		t.Fatalf("canonical field lost: %+v", got)
	}
	if _, ok := got.Utilities["water_vnd"]; ok {
		t.Fatalf("unknown utility was retained: %+v", got.Utilities)
	}
	if _, ok := got.Amenities["sea_view"]; ok {
		t.Fatalf("unknown amenity was retained: %+v", got.Amenities)
	}
	if _, ok := got.Restrictions["smoking_allowed"]; ok {
		t.Fatalf("unknown restriction was retained: %+v", got.Restrictions)
	}
	if _, ok := got.Confidence["group_name"]; ok {
		t.Fatalf("unknown confidence was retained: %+v", got.Confidence)
	}
	if len(got.Utilities) != 6 || len(got.Amenities) != 9 || len(got.Restrictions) != 1 {
		t.Fatalf("nested canonical schema was not restored: %+v", got)
	}
}

func TestStrictSchemaNormalizationStillRejectsUnknownFields(t *testing.T) {
	canonical := map[string]any{}
	for _, key := range enrichmentKeys() {
		canonical[key] = nil
	}
	canonical["is_rental_listing"] = true
	canonical["district"] = "Unknown"
	canonical["utilities"] = map[string]any{"water_vnd": 100000}
	canonical["amenities"] = map[string]any{}
	canonical["restrictions"] = map[string]any{}
	canonical["confidence"] = map[string]any{}
	raw, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = normalizeEnrichmentJSON(raw, false); err == nil || !strings.Contains(err.Error(), "unknown field water_vnd") {
		t.Fatalf("strict mode accepted unknown nested field: %v", err)
	}

	canonical["utilities"] = map[string]any{}
	canonical["group_name"] = "unexpected"
	raw, _ = json.Marshal(canonical)
	if _, err = normalizeEnrichmentJSON(raw, false); err == nil || !strings.Contains(err.Error(), "unknown field group_name") {
		t.Fatalf("strict mode accepted unknown top-level field: %v", err)
	}
}

func TestCompatibleNullsImpossibleOptionalType(t *testing.T) {
	content := `{"is_rental_listing":true,"bedrooms":"two"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()
	got, err := New(Config{Provider: "compatible", BaseURL: srv.URL, APIKey: "x", Model: "m"}).Enrich(context.Background(), "x", domain.Listing{})
	if err != nil || got.Bedrooms != nil {
		t.Fatalf("bedrooms=%v err=%v", got.Bedrooms, err)
	}
}

func TestCompatibleSchemaAwareNormalizerProductionShapes(t *testing.T) {
	content := `{
		"is_rental_listing":true,
		"rent_vnd":{"value":6500000},
		"rent_max_vnd":7000000,
		"bedrooms":{"value":2},
		"property_type":{"name":"apartment"},
		"district":"son tra",
		"location_original":false,
		"ward":["An Hai"],
		"street":123,
		"address":{"text":"My Khe"},
		"building":{"name":"Ocean"},
		"furnished":["full"],
		"near_beach":"yes",
		"utilities":{"electricity_vnd_per_kwh":"4000","water_vnd_per_month":100000},
		"amenities":{"balcony":"yes","elevator":true,"pool":{"available":true}},
		"restrictions":{"electric_bike_allowed":"unknown"},
		"confidence":{"rent_vnd":{"score":0.9},"district":0.8,"address":"high"}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()
	got, err := New(Config{Provider: "compatible", BaseURL: srv.URL, APIKey: "x", Model: "m"}).Enrich(context.Background(), "rental", domain.Listing{})
	if err != nil {
		t.Fatal(err)
	}
	if got.RentMinVND != nil || got.RentMaxVND == nil || *got.RentMaxVND != 7_000_000 || got.Bedrooms != nil || got.PropertyType != nil {
		t.Fatalf("numeric/enum normalization failed: %+v", got)
	}
	if got.District == nil || *got.District != domain.DistrictSonTra {
		t.Fatalf("district=%v", got.District)
	}
	if got.LocationOriginal != nil || got.Ward != nil || got.Street != nil || got.Address != nil || got.Building != nil {
		t.Fatalf("location strings were not nulled: %+v", got)
	}
	if got.Furnished != nil || got.NearBeach != nil {
		t.Fatalf("optional enum/bool was not nulled: furnished=%v near_beach=%v", got.Furnished, got.NearBeach)
	}
	if got.Confidence["rent_vnd"] != 0 || got.Confidence["address"] != 0 || got.Confidence["district"] != .8 {
		t.Fatalf("confidence=%v", got.Confidence)
	}
	if got.Amenities["balcony"] != nil || got.Amenities["pool"] != nil || got.Amenities["elevator"] != true {
		t.Fatalf("amenities=%v", got.Amenities)
	}
	if got.Utilities["electricity_vnd_per_kwh"] != nil || got.Utilities["water_vnd_per_month"] != float64(100000) {
		t.Fatalf("utilities=%v", got.Utilities)
	}
	if got.Restrictions["electric_bike_allowed"] != nil {
		t.Fatalf("restrictions=%v", got.Restrictions)
	}
}

func TestCompatibleUnknownOrWrongDistrictBecomesUnknown(t *testing.T) {
	for _, district := range []string{`"Atlantis"`, `{"name":"Son Tra"}`, `42`, `null`} {
		content := `{"is_rental_listing":true,"district":` + district + `}`
		normalized, err := normalizeEnrichmentJSON([]byte(content), true)
		if err != nil {
			t.Fatalf("district=%s err=%v", district, err)
		}
		var got domain.Enrichment
		if err = json.Unmarshal(normalized, &got); err != nil || got.District == nil || *got.District != domain.DistrictUnknown {
			t.Fatalf("district=%s got=%+v err=%v", district, got.District, err)
		}
	}
}

func TestCompatibleStillRejectsUnsafeRentalFlagAndMalformedJSON(t *testing.T) {
	for _, content := range []string{`{"is_rental_listing":"true","rent_vnd":6500000}`, `{"is_rental_listing":{"value":true}}`, `{`} {
		if _, err := normalizeEnrichmentJSON([]byte(content), true); err == nil {
			t.Fatalf("accepted unsafe response %s", content)
		}
	}
}

func TestStrictModeDoesNotSanitizeWrongOptionalTypes(t *testing.T) {
	content := canonicalEnrichmentTemplate()
	content["address"] = map[string]any{"text": "My Khe"}
	content["confidence"] = map[string]any{"address": map[string]any{"score": .9}}
	raw, _ := json.Marshal(content)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(raw)}}}})
	}))
	defer srv.Close()
	if _, err := New(Config{Provider: "openai", BaseURL: srv.URL, APIKey: "x", Model: "strict"}).Enrich(context.Background(), "rental", domain.Listing{}); err == nil {
		t.Fatal("strict mode sanitized invalid optional fields")
	}
}

func TestCompatibleInfersMissingRentalFlagConservatively(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantValue *bool
		wantError bool
	}{
		{name: "rent and property", content: `{"rent_vnd":6500000,"property_type":"apartment"}`, wantValue: boolPtr(true)},
		{name: "rent only", content: `{"rent_vnd":6500000}`, wantError: true},
		{name: "location only", content: `{"district":"Son Tra","location_original":"My Khe"}`, wantError: true},
		{name: "explicit true", content: `{"is_rental_listing":true}`, wantValue: boolPtr(true)},
		{name: "explicit false", content: `{"is_rental_listing":false}`, wantValue: boolPtr(false)},
		{name: "wrong type", content: `{"is_rental_listing":"yes","rent_vnd":6500000,"property_type":"apartment"}`, wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": tc.content}}}})
			}))
			defer srv.Close()
			got, err := New(Config{Provider: "compatible", BaseURL: srv.URL, APIKey: "x", Model: "m"}).Enrich(context.Background(), "rental source", domain.Listing{})
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected failure, got %+v", got)
				}
				return
			}
			if err != nil || got.IsRental == nil || *got.IsRental != *tc.wantValue {
				t.Fatalf("is_rental=%v err=%v", got.IsRental, err)
			}
		})
	}
}

func TestStrictModeStillRequiresRentalFlag(t *testing.T) {
	canonical := map[string]any{}
	for _, key := range enrichmentKeys() {
		if key != "is_rental_listing" {
			canonical[key] = nil
		}
	}
	raw, _ := json.Marshal(canonical)
	if _, err := normalizeEnrichmentJSON(raw, false); err == nil || !strings.Contains(err.Error(), "missing is_rental_listing") {
		t.Fatalf("strict mode accepted missing rental flag: %v", err)
	}
}

func TestRateLimitTimingParsing(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	retryAt := now.Add(15 * time.Second)
	raw := []byte(fmt.Sprintf(`{"error":{"retryAtMs":%d}}`, retryAt.UnixMilli()))
	err := newHTTPError(http.StatusTooManyRequests, raw, http.Header{"Retry-After": []string{"5"}}, now)
	at, after, ok := RateLimitReset(err)
	if !ok || !at.Equal(retryAt) || after != 15*time.Second {
		t.Fatalf("at=%v after=%v ok=%v", at, after, ok)
	}
	relative := newHTTPError(http.StatusTooManyRequests, []byte(`{"retryAtMs":"28800000"}`), nil, now)
	at, after, ok = RateLimitReset(relative)
	if !ok || !at.Equal(now.Add(8*time.Hour)) || after != 8*time.Hour {
		t.Fatalf("relative at=%v after=%v ok=%v", at, after, ok)
	}
}

func boolPtr(value bool) *bool { return &value }
