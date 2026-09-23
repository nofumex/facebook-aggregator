package exchange

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/charmap"
)

const dailyURL = "https://www.cbr.ru/scripts/XML_daily.asp"

type Provider interface {
	VNDToRUB(context.Context) (float64, error)
}

// CBR reads the official daily VND/RUB rate and caches it. If a refresh fails,
// the last successfully loaded rate remains usable instead of making cards
// lose their rouble equivalent during a transient outage.
type CBR struct {
	client *http.Client
	url    string
	ttl    time.Duration
	mu     sync.Mutex
	rate   float64
	until  time.Time
}

func NewCBR() *CBR {
	return &CBR{client: &http.Client{Timeout: 5 * time.Second}, url: dailyURL, ttl: 12 * time.Hour}
}

func (c *CBR) VNDToRUB(ctx context.Context) (float64, error) {
	c.mu.Lock()
	if c.rate > 0 && time.Now().Before(c.until) {
		rate := c.rate
		c.mu.Unlock()
		return rate, nil
	}
	stale := c.rate
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return staleOrError(stale, err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return staleOrError(stale, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return staleOrError(stale, fmt.Errorf("CBR HTTP %d", resp.StatusCode))
	}
	var feed struct {
		Currencies []struct {
			Code    string `xml:"CharCode"`
			Nominal int64  `xml:"Nominal"`
			Value   string `xml:"Value"`
		} `xml:"Valute"`
	}
	decoder := xml.NewDecoder(resp.Body)
	decoder.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "windows-1251") {
			return charmap.Windows1251.NewDecoder().Reader(input), nil
		}
		return nil, fmt.Errorf("unsupported CBR XML charset %q", charset)
	}
	if err = decoder.Decode(&feed); err != nil {
		return staleOrError(stale, err)
	}
	for _, v := range feed.Currencies {
		if v.Code != "VND" || v.Nominal <= 0 {
			continue
		}
		value, parseErr := strconv.ParseFloat(strings.ReplaceAll(v.Value, ",", "."), 64)
		if parseErr != nil || value <= 0 {
			return staleOrError(stale, errors.New("invalid VND rate in CBR response"))
		}
		rate := value / float64(v.Nominal)
		c.mu.Lock()
		c.rate, c.until = rate, time.Now().Add(c.ttl)
		c.mu.Unlock()
		return rate, nil
	}
	return staleOrError(stale, errors.New("VND rate is absent from CBR response"))
}

func staleOrError(stale float64, err error) (float64, error) {
	if stale > 0 {
		return stale, nil
	}
	return 0, err
}
