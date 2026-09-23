package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestCompatibleRejectsImpossibleType(t *testing.T) {
	content := `{"is_rental_listing":true,"bedrooms":"two"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer srv.Close()
	_, err := New(Config{Provider: "compatible", BaseURL: srv.URL, APIKey: "x", Model: "m"}).Enrich(context.Background(), "x", domain.Listing{})
	if err == nil {
		t.Fatal("invalid type accepted")
	}
}
