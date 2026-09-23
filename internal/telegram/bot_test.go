package telegram

import (
	"context"
	"encoding/json"
	"github.com/egori/facebook-aggregator/internal/domain"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMainMenuAndCardEditFlows(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["method_path"] = r.URL.Path
		mu.Lock()
		calls = append(calls, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer srv.Close()
	b := &Bot{api: &Client{base: srv.URL, http: srv.Client()}, admins: map[int64]bool{42: true}, log: slog.New(slog.NewTextHandler(io.Discard, nil)), states: map[int64]string{}, filters: map[int64]domain.SearchFilter{}, pages: map[string]pageCache{}}
	b.showMenu(context.Background(), 42, 10)
	rent := int64(5_000_000)
	beds := 2
	area := 50.0
	b.pages["token"] = pageCache{items: []domain.Listing{{ID: 1, FacebookURL: "https://facebook.com/post", RentMin: &rent, RentMax: &rent, Bedrooms: &beds, AreaM2: &area, District: "Sơn Trà", PropertyType: "apartment", PublishedAt: time.Now(), DealScore: 91, Utilities: map[string]any{}}}, total: 1, user: 42, until: time.Now().Add(time.Minute), title: "🏠 Новые"}
	b.renderCard(context.Background(), 42, 10, "token", 0, false)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("calls=%d", len(calls))
	}
	menu, _ := calls[0]["text"].(string)
	card, _ := calls[1]["text"].(string)
	if !strings.Contains(menu, "Аренда в Дананге") || !strings.Contains(card, "91/100") || calls[0]["method_path"] != "/editMessageText" {
		t.Fatalf("menu=%q card=%q calls=%v", menu, card, calls)
	}
}

func TestListingWithPhotoRenderedAsPhotoCard(t *testing.T) {
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["method_path"] = r.URL.Path
		calls = append(calls, body)
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":77}}`)
	}))
	defer srv.Close()
	b := testBot(srv)
	b.pages["p"] = pageCache{items: []domain.Listing{{ID: 1, FacebookURL: "https://facebook.com/post", MediaURLs: []string{"https://scontent.fbcdn.net/one.jpg"}, PublishedAt: time.Now(), Utilities: map[string]any{}}}, total: 1, title: "Фото"}
	b.renderCard(context.Background(), 1, 0, "p", 0, false)
	if len(calls) != 1 || calls[0]["method_path"] != "/sendPhoto" || calls[0]["photo"] != "https://scontent.fbcdn.net/one.jpg" {
		t.Fatalf("calls=%v", calls)
	}
	if caption, _ := calls[0]["caption"].(string); !strings.Contains(caption, "Deal score") {
		t.Fatalf("caption=%q", caption)
	}
}

func TestNavigationPhotoToPhotoUsesEditMessageMedia(t *testing.T) {
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["method_path"] = r.URL.Path
		calls = append(calls, body)
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":10}}`)
	}))
	defer srv.Close()
	b := testBot(srv)
	b.pages["p"] = pageCache{items: []domain.Listing{{ID: 1, MediaURLs: []string{"https://scontent.fbcdn.net/one.jpg"}, PublishedAt: time.Now(), Utilities: map[string]any{}}, {ID: 2, MediaURLs: []string{"https://scontent.fbcdn.net/two.jpg"}, PublishedAt: time.Now(), Utilities: map[string]any{}}}, total: 2, title: "Фото"}
	b.renderCard(context.Background(), 1, 10, "p", 1, true)
	if len(calls) != 1 || calls[0]["method_path"] != "/editMessageMedia" {
		t.Fatalf("calls=%v", calls)
	}
	media := calls[0]["media"].(map[string]any)
	if media["media"] != "https://scontent.fbcdn.net/two.jpg" {
		t.Fatalf("media=%v", media)
	}
}

func TestNavigationPhotoToTextReplacesMessage(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":12}}`)
	}))
	defer srv.Close()
	b := testBot(srv)
	b.pages["p"] = pageCache{items: []domain.Listing{{ID: 1, MediaURLs: []string{"https://scontent.fbcdn.net/one.jpg"}}, {ID: 2, PublishedAt: time.Now(), Utilities: map[string]any{}}}, total: 2, title: "Фото"}
	b.renderCard(context.Background(), 1, 10, "p", 1, true)
	if strings.Join(paths, ",") != "/deleteMessage,/sendMessage" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestUnavailablePhotoFallsBackToTextCard(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/sendPhoto" {
			_, _ = io.WriteString(w, `{"ok":false,"description":"failed to get HTTP URL content"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":11}}`)
	}))
	defer srv.Close()
	b := testBot(srv)
	b.pages["p"] = pageCache{items: []domain.Listing{{ID: 1, MediaURLs: []string{"https://scontent.fbcdn.net/expired.jpg"}, PublishedAt: time.Now(), Utilities: map[string]any{}}}, total: 1, title: "Фото"}
	b.renderCard(context.Background(), 1, 0, "p", 0, false)
	if strings.Join(paths, ",") != "/sendPhoto,/sendMessage" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestSendMediaGroupAcceptsOneThroughTen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sendPhoto" {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
	}))
	defer srv.Close()
	client := &Client{base: srv.URL, http: srv.Client()}
	for n := 1; n <= 10; n++ {
		media := make([]InputMediaPhoto, n)
		for i := range media {
			media[i] = InputMediaPhoto{Type: "photo", Media: "https://scontent.fbcdn.net/x.jpg"}
		}
		if _, err := client.SendMediaGroup(context.Background(), 1, media); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
	if _, err := client.SendMediaGroup(context.Background(), 1, make([]InputMediaPhoto, 11)); err == nil {
		t.Fatal("11-item media group must be rejected")
	}
}

func TestAlbumsOverTenAreSplit(t *testing.T) {
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if media, ok := body["media"].([]any); ok {
			sizes = append(sizes, len(media))
		} else {
			sizes = append(sizes, 1)
		}
		if r.URL.Path == "/sendPhoto" {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
		} else {
			_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
		}
	}))
	defer srv.Close()
	photos := make([]string, 21)
	for i := range photos {
		photos[i] = "https://scontent.fbcdn.net/photo.jpg"
	}
	if err := sendAlbumsResilient(context.Background(), &Client{base: srv.URL, http: srv.Client()}, 1, photos); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 3 || sizes[0] != 10 || sizes[1] != 10 || sizes[2] != 1 {
		t.Fatalf("sizes=%v", sizes)
	}
}

func TestUnavailableAlbumImageDoesNotBlockGoodImages(t *testing.T) {
	goodSingles := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "bad.jpg") {
			_, _ = io.WriteString(w, `{"ok":false,"description":"bad image"}`)
			return
		}
		if r.URL.Path == "/sendPhoto" {
			goodSingles++
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
		} else {
			_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
		}
	}))
	defer srv.Close()
	err := sendAlbumsResilient(context.Background(), &Client{base: srv.URL, http: srv.Client()}, 1, []string{"https://scontent.fbcdn.net/good1.jpg", "https://scontent.fbcdn.net/bad.jpg", "https://scontent.fbcdn.net/good2.jpg"})
	if err == nil || goodSingles != 2 {
		t.Fatalf("err=%v goodSingles=%d", err, goodSingles)
	}
}

func testBot(srv *httptest.Server) *Bot {
	return &Bot{api: &Client{base: srv.URL, http: srv.Client()}, admins: map[int64]bool{}, log: slog.New(slog.NewTextHandler(io.Discard, nil)), states: map[int64]string{}, filters: map[int64]domain.SearchFilter{}, pages: map[string]pageCache{}}
}

func TestCardAddsRoubleEquivalent(t *testing.T) {
	rent := int64(8_000_000)
	text := card(domain.Listing{RentMin: &rent, RentMax: &rent, PublishedAt: time.Now(), Utilities: map[string]any{}}, .003125)
	if !strings.Contains(text, "8 000 000 ₫ (≈ 25 000 ₽)") {
		t.Fatalf("card=%q", text)
	}
}

func TestPhotoURLsKeepsFacebookImagesOnly(t *testing.T) {
	photo := "https://scontent.xx.fbcdn.net/v/room.jpg?x=1"
	got := photoURLs([]string{photo, photo, "https://facebook.com/groups/1/posts/2", "https://video.xx.fbcdn.net/clip.mp4"})
	if len(got) != 1 || got[0] != photo {
		t.Fatalf("photos=%v", got)
	}
}
