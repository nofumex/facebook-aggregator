package storage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/ranking"
	"github.com/egori/facebook-aggregator/internal/storage"
	"github.com/egori/facebook-aggregator/migrations"
	"os"
	"testing"
	"time"
)

func TestPostgresDedupAndSearch(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, e := storage.Open(ctx, url, 3, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.DB.Config().MaxConns != 3 || s.DB.Config().MinConns != 1 {
		t.Fatalf("pool max=%d min=%d", s.DB.Config().MaxConns, s.DB.Config().MinConns)
	}
	if e = migrations.Up(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	g, e := s.AddGroup(ctx, "fb-"+suffix, "https://example.invalid/"+suffix, "test", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DeleteGroup(ctx, g.ID)
	rent := int64(5_000_000)
	beds := 2
	area := 50.0
	post := domain.FacebookPost{ID: "post-" + suffix, GroupID: g.FacebookID, URL: "https://example.invalid/post", Text: "2PN 50m2 5tr", PublishedAt: time.Now(), Raw: json.RawMessage(`{"ok":true}`)}
	listing := domain.Listing{GroupID: g.ID, FacebookPostID: post.ID, FacebookURL: post.URL, OriginalText: post.Text, PublishedAt: post.PublishedAt, RentMin: &rent, RentMax: &rent, Bedrooms: &beds, AreaM2: &area, District: "Son Tra", PropertyType: "apartment", Currency: "VND", Amenities: map[string]bool{}, Utilities: map[string]any{}, RawValues: map[string]any{}, Confidence: domain.Confidence{}, DealScore: 90, ScoreConfidence: .8}
	inserted, e := s.InsertListing(ctx, post, listing)
	if e != nil || !inserted {
		t.Fatalf("first insert=%v err=%v", inserted, e)
	}
	inserted, e = s.InsertListing(ctx, post, listing)
	if e != nil || inserted {
		t.Fatalf("duplicate insert=%v err=%v", inserted, e)
	}
	page, e := s.Search(ctx, 999, domain.SearchFilter{District: "Son Tra", RentMax: &rent, Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, x := range page.Items {
		if x.FacebookPostID == post.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("inserted listing absent from search: total=%d", page.Total)
	}
}

func TestRerankPeriodIncludesStrongListingBeyondNewestFiveHundred(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, e := storage.Open(ctx, url, 3, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = migrations.Up(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	g, e := s.AddGroup(ctx, "bulk-"+suffix, "https://example.invalid/"+suffix, "bulk", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DeleteGroup(ctx, g.ID)
	_, e = s.DB.Exec(ctx, `INSERT INTO posts(group_id,facebook_post_id,facebook_url,original_text,published_at,content_hash) SELECT $1,'bulk-'||$2::text||'-'||n::text,'https://example.invalid/'||n::text,'1BR apartment',CASE WHEN n=501 THEN now()-interval '3 days' ELSE now()-n*interval '1 minute' END,decode(md5($2::text||n::text),'hex') FROM generate_series(1,501)n`, g.ID, suffix)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec(ctx, `INSERT INTO listings(post_id,rent_min,rent_max,currency,is_rental,bedrooms,area_m2,property_type,district,furnished,amenities,utilities,restrictions,raw_values,confidence,parser_version,extraction_status) SELECT p.id,CASE WHEN p.facebook_post_id LIKE '%-501' THEN 3000000 ELSE 7000000 END,CASE WHEN p.facebook_post_id LIKE '%-501' THEN 3000000 ELSE 7000000 END,'VND',true,1,40,'apartment','Son Tra','partial','{}','{}','{}','{}','{"price":0.95,"district":0.95,"property_type":0.95,"bedrooms":0.95,"area_m2":0.95,"furnished":0.95}','test','success' FROM posts p WHERE p.group_id=$1`, g.ID)
	if e != nil {
		t.Fatal(e)
	}
	updated, e := s.RerankPeriod(ctx, time.Now().Add(-7*24*time.Hour), ranking.New(), 100)
	if e != nil {
		t.Fatal(e)
	}
	if updated < 501 {
		t.Fatalf("updated=%d", updated)
	}
	page, e := s.Search(ctx, 0, domain.SearchFilter{FreshAfter: ptrTime(time.Now().Add(-7 * 24 * time.Hour)), Sort: "score", Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 || page.Items[0].FacebookPostID != "bulk-"+suffix+"-501" {
		t.Fatalf("top=%+v", page.Items)
	}
}

func ptrTime(v time.Time) *time.Time { return &v }
