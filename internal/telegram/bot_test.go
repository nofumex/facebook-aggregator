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
	b.renderCard(context.Background(), 42, 10, "token", 0)
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
