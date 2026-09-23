package exchange

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVNDToRUBParsesNominalAndCaches(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="windows-1251"?><ValCurs><Valute><CharCode>VND</CharCode><Nominal>10000</Nominal><Value>31,2500</Value></Valute></ValCurs>`)
	}))
	defer srv.Close()
	c := &CBR{client: srv.Client(), url: srv.URL, ttl: time.Hour}
	for range 2 {
		rate, err := c.VNDToRUB(context.Background())
		if err != nil || rate != .003125 {
			t.Fatalf("rate=%v err=%v", rate, err)
		}
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
