package groups

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
)

const bootstrapURL = "https://www.facebook.com/groups/feed/"

// sessionState holds the per-process session tokens extracted from the initial
// page load. All fields are protected by mu; callers must hold at least a read
// lock before accessing any field.
type sessionState struct {
	mu      sync.RWMutex
	fbDTSG  string // fb_dtsg: CSRF token for every GraphQL POST
	lsd     string // lsd: lightweight session data (X-FB-LSD header + form body)
	rev     string // __rev / __spin_r: client revision number
	hs      string // __hs: haste_session
	hsi     string // __hsi: haste_session instance
	spinT   string // __spin_t: spin timestamp
	spinS   string // __s: spin session (optional; omitted when absent)
	jazoest string // jazoest: computed anti-bot field
}

// Regexes for token extraction from the HTML page source.
var (
	reDTSG  = regexp.MustCompile(`"DTSGInitialData"[^{]*\{"token":"([^"]+)"`)
	reLSD   = regexp.MustCompile(`"LSD"[^{]*\{"token":"([^"]+)"`)
	reRev   = regexp.MustCompile(`"client_revision"\s*:\s*(\d+)`)
	reHs    = regexp.MustCompile(`"haste_session"\s*:\s*"([^"]+)"`)
	reHsi   = regexp.MustCompile(`"hsi"\s*:\s*"(\d+)"`)
	reSpinT = regexp.MustCompile(`"_?_?spin_t"\s*:\s*(\d+)`)
	reSpinS = regexp.MustCompile(`"_?_?spin_s"\s*:\s*"([^"]+)"`)
	reDocID = regexp.MustCompile(`"queryID"\s*:\s*"(\d{10,})"`)
	reQName = regexp.MustCompile(`"((?:Groups|Comet|Comment|Composer|Feedback|Member|UFI)[A-Za-z_]+(?:Query|Mutation))"`)
)

// bootstrap performs a single authenticated GET to /groups/feed/ and extracts
// the session tokens required for every subsequent GraphQL request.
func (c *Client) bootstrap() error {
	req, err := http.NewRequest(http.MethodGet, bootstrapURL, nil)
	if err != nil {
		return fmt.Errorf("%w: building bootstrap request: %v", ErrRequestFailed, err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Cookie", c.cookieHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: bootstrap GET failed: %v", ErrRequestFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: bootstrap returned HTTP %d", ErrRequestFailed, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: reading bootstrap body: %v", ErrRequestFailed, err)
	}

	return c.parseSessionTokens(body)
}

// parseSessionTokens extracts all session tokens from an HTML page body and
// atomically updates sessionState.
func (c *Client) parseSessionTokens(body []byte) error {
	extract := func(re *regexp.Regexp, required bool, name string) (string, error) {
		m := re.FindSubmatch(body)
		if m == nil {
			if required {
				return "", fmt.Errorf("%w: could not extract %s from page — session may be invalid", ErrUnauthorized, name)
			}
			return "", nil
		}
		return string(m[1]), nil
	}

	dtsg, err := extract(reDTSG, true, "fb_dtsg")
	if err != nil {
		return err
	}
	lsd, err := extract(reLSD, true, "lsd")
	if err != nil {
		return err
	}
	rev, err := extract(reRev, true, "__rev")
	if err != nil {
		return err
	}
	hs, err := extract(reHs, true, "__hs")
	if err != nil {
		return err
	}
	hsi, err := extract(reHsi, true, "__hsi")
	if err != nil {
		return err
	}
	spinT, err := extract(reSpinT, true, "__spin_t")
	if err != nil {
		return err
	}
	spinS, _ := extract(reSpinS, false, "__spin_s")

	c.session.mu.Lock()
	c.session.fbDTSG = dtsg
	c.session.lsd = lsd
	c.session.rev = rev
	c.session.hs = hs
	c.session.hsi = hsi
	c.session.spinT = spinT
	c.session.spinS = spinS
	c.session.jazoest = computeJazoest(dtsg)
	c.session.mu.Unlock()

	return nil
}

// computeJazoest derives the jazoest anti-bot field from fb_dtsg by summing
// the unicode code points of each rune and prepending "2".
func computeJazoest(token string) string {
	sum := 0
	for _, r := range token {
		sum += int(r)
	}
	return "2" + strconv.Itoa(sum)
}

// snap returns a consistent copy of all session fields under the read lock.
func (s *sessionState) snap() (dtsg, lsd, rev, hs, hsi, spinT, spinS, jazoest string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fbDTSG, s.lsd, s.rev, s.hs, s.hsi, s.spinT, s.spinS, s.jazoest
}
