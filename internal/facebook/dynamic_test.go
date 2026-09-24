package facebook

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egori/facebook-aggregator/internal/domain"
)

func TestParseCookiesDevToolsWhitespaceAndRoundTrip(t *testing.T) {
	raw := strings.Join([]string{
		"sb\tsb-value\t.facebook.com\t/",
		"datr datr-value .facebook.com /",
		"c_user\t123456\t.facebook.com\t/",
		"xs xs-value%3Awith=equals .facebook.com /",
		"fr fr-value",
		"ps_l 1",
		"ps_n 1",
	}, "\n")
	cookies, err := ParseCookies(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cookies.SB != "sb-value" || cookies.DATR != "datr-value" || cookies.CUser != "123456" || cookies.XS != "xs-value%3Awith=equals" || cookies.FR != "fr-value" || cookies.PSL != "1" || cookies.PSN != "1" {
		t.Fatalf("unexpected parsed cookie names")
	}
	encoded, err := MarshalCookies(cookies)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalCookies(encoded)
	if err != nil || restored != cookies {
		t.Fatalf("roundtrip mismatch: err=%v", err)
	}
}

func TestParseCookiesRequiresAuthenticationPair(t *testing.T) {
	if _, err := ParseCookies("sb value\ndatr value"); err == nil || strings.Contains(err.Error(), "value") {
		t.Fatalf("unsafe or overly detailed error: %v", err)
	}
}

type adapterStub struct {
	err  error
	name string
}

func (a adapterStub) ResolveGroup(context.Context, string) (string, string, string, error) {
	return "", "", "", a.err
}
func (a adapterStub) FetchRecent(context.Context, FetchRequest) (FetchResult, error) {
	return FetchResult{Posts: []domain.FacebookPost{{ID: a.name}}}, a.err
}
func (a adapterStub) Check(context.Context, string) error { return a.err }
func (a adapterStub) Name() string                        { return a.name }

func TestDynamicAdapterReportsAuthAndHotSwaps(t *testing.T) {
	dynamic := NewDynamicAdapter(adapterStub{err: ErrAuthentication, name: "expired"})
	var alerts atomic.Int64
	dynamic.SetAuthenticationHandler(func(error) { alerts.Add(1) })
	if _, err := dynamic.FetchRecent(context.Background(), FetchRequest{}); !errors.Is(err, ErrAuthentication) || alerts.Load() != 1 {
		t.Fatalf("err=%v alerts=%d", err, alerts.Load())
	}
	dynamic.Replace(adapterStub{name: "fresh"})
	result, err := dynamic.FetchRecent(context.Background(), FetchRequest{})
	if err != nil || dynamic.Name() != "fresh" || len(result.Posts) != 1 || result.Posts[0].ID != "fresh" || alerts.Load() != 1 {
		t.Fatalf("result=%+v err=%v alerts=%d", result, err, alerts.Load())
	}
}

func TestClassifyRecognizesProductionAuthMessages(t *testing.T) {
	for _, message := range []string{"error 1357001", "Log in to continue", "Not logged in", "login required", "invalid session", "OAuthException", "authentication failed"} {
		if err := classify(errors.New(message)); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("message=%q err=%v", message, err)
		}
	}
}
