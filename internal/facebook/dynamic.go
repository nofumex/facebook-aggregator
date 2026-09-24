package facebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/teslashibe/facebook-go/groups"
)

// DynamicAdapter lets all consumers observe an atomically replaced Facebook
// session without recreating sync workers or the Telegram bot.
type DynamicAdapter struct {
	mu         sync.RWMutex
	active     Adapter
	onAuth     func(error)
	generation uint64
}

func NewDynamicAdapter(initial Adapter) *DynamicAdapter {
	return &DynamicAdapter{active: initial}
}

func (a *DynamicAdapter) Replace(next Adapter) {
	if next == nil {
		return
	}
	a.mu.Lock()
	a.active = next
	a.generation++
	a.mu.Unlock()
}

func (a *DynamicAdapter) SetAuthenticationHandler(handler func(error)) {
	a.mu.Lock()
	a.onAuth = handler
	a.mu.Unlock()
}

func (a *DynamicAdapter) current() (Adapter, func(error), uint64) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.active, a.onAuth, a.generation
}

func (a *DynamicAdapter) observe(err error, handler func(error), generation uint64) {
	a.mu.RLock()
	currentGeneration := a.generation
	a.mu.RUnlock()
	if generation == currentGeneration && errors.Is(err, ErrAuthentication) && handler != nil {
		handler(err)
	}
}

func (a *DynamicAdapter) ResolveGroup(ctx context.Context, ref string) (string, string, string, error) {
	current, handler, generation := a.current()
	id, name, url, err := current.ResolveGroup(ctx, ref)
	a.observe(err, handler, generation)
	return id, name, url, err
}

func (a *DynamicAdapter) FetchRecent(ctx context.Context, request FetchRequest) (FetchResult, error) {
	current, handler, generation := a.current()
	result, err := current.FetchRecent(ctx, request)
	a.observe(err, handler, generation)
	return result, err
}

func (a *DynamicAdapter) Check(ctx context.Context, groupID string) error {
	current, handler, generation := a.current()
	err := current.Check(ctx, groupID)
	a.observe(err, handler, generation)
	return err
}

func (a *DynamicAdapter) Name() string {
	current, _, _ := a.current()
	return current.Name()
}

var cookieNames = map[string]func(*groups.Cookies, string){
	"sb":     func(c *groups.Cookies, value string) { c.SB = value },
	"datr":   func(c *groups.Cookies, value string) { c.DATR = value },
	"c_user": func(c *groups.Cookies, value string) { c.CUser = value },
	"xs":     func(c *groups.Cookies, value string) { c.XS = value },
	"fr":     func(c *groups.Cookies, value string) { c.FR = value },
	"ps_l":   func(c *groups.Cookies, value string) { c.PSL = value },
	"ps_n":   func(c *groups.Cookies, value string) { c.PSN = value },
}

// ParseCookies accepts the whitespace/tabular form copied from browser
// DevTools and also tolerates a standard semicolon-separated Cookie header.
func ParseCookies(input string) (groups.Cookies, error) {
	values := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(input, ";", "\n"), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		for i := 0; i < len(fields); i++ {
			name, value := "", ""
			if key, raw, ok := strings.Cut(fields[i], "="); ok {
				name, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(raw)
			} else if _, known := cookieNames[strings.ToLower(fields[i])]; known && i+1 < len(fields) {
				name, value = strings.ToLower(fields[i]), fields[i+1]
				i++
			}
			if _, known := cookieNames[name]; known && value != "" {
				values[name] = value
			}
		}
	}
	var cookies groups.Cookies
	for name, setter := range cookieNames {
		setter(&cookies, values[name])
	}
	if cookies.CUser == "" || cookies.XS == "" {
		return groups.Cookies{}, fmt.Errorf("cookie table must contain c_user and xs")
	}
	return cookies, nil
}

func MarshalCookies(cookies groups.Cookies) (string, error) {
	encoded, err := json.Marshal(map[string]string{"sb": cookies.SB, "datr": cookies.DATR, "c_user": cookies.CUser, "xs": cookies.XS, "fr": cookies.FR, "ps_l": cookies.PSL, "ps_n": cookies.PSN})
	return string(encoded), err
}

func UnmarshalCookies(encoded string) (groups.Cookies, error) {
	var values map[string]string
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return groups.Cookies{}, fmt.Errorf("decode stored Facebook cookies: %w", err)
	}
	var cookies groups.Cookies
	for name, setter := range cookieNames {
		setter(&cookies, values[name])
	}
	if cookies.CUser == "" || cookies.XS == "" {
		return groups.Cookies{}, fmt.Errorf("stored Facebook cookies are incomplete")
	}
	return cookies, nil
}
