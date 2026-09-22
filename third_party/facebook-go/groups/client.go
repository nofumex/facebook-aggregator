package groups

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// graphql executes an authenticated Facebook GraphQL POST request.
// It handles rate limiting, retries, session refresh, and the for(;;); prefix.
func (c *Client) graphql(ctx context.Context, friendlyName string, variables interface{}) (json.RawMessage, error) {
	docID := c.docID(friendlyName)
	if docID == "" {
		return nil, fmt.Errorf("%w: no doc_id registered for %q", ErrInvalidParams, friendlyName)
	}

	varsJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, fmt.Errorf("%w: marshalling variables: %v", ErrInvalidParams, err)
	}

	attempts := c.maxRetries
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	bootstrapped := false
	for i := 0; i < attempts; i++ {
		if i > 0 {
			wait := c.retryBase * time.Duration(math.Pow(2, float64(i-1)))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		data, err := c.doGraphQL(ctx, friendlyName, docID, varsJSON)
		if err == nil {
			return data, nil
		}

		// On session expiry, re-bootstrap exactly once then retry.
		if errors.Is(err, ErrSessionExpired) && !bootstrapped {
			bootstrapped = true
			if bErr := c.bootstrap(); bErr == nil {
				continue
			}
			return nil, err
		}

		if isNonRetriable(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// doGraphQLRaw performs a single POST and returns the raw response body
// (with for(;;); stripped) for callers that need to parse multiple lines.
func (c *Client) doGraphQLRaw(ctx context.Context, friendlyName, docID string, varsJSON []byte) ([]byte, error) {
	c.waitForGap(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	dtsg, lsd, rev, hs, hsi, spinT, spinS, jazoest := c.session.snap()
	req64 := strconv.FormatUint(c.reqCounter.Add(1), 16)

	form := url.Values{}
	form.Set("av", c.cookies.CUser)
	form.Set("__user", c.cookies.CUser)
	form.Set("__a", "1")
	form.Set("__req", req64)
	form.Set("__hs", hs)
	form.Set("dpr", "2")
	form.Set("__ccg", "EXCELLENT")
	form.Set("__rev", rev)
	if spinS != "" {
		form.Set("__s", spinS)
	}
	form.Set("__hsi", hsi)
	form.Set("__comet_req", "15")
	form.Set("fb_dtsg", dtsg)
	form.Set("jazoest", jazoest)
	form.Set("lsd", lsd)
	form.Set("__spin_r", rev)
	form.Set("__spin_b", "trunk")
	form.Set("__spin_t", spinT)
	form.Set("server_timestamps", "true")
	form.Set("fb_api_caller_class", "RelayModern")
	form.Set("fb_api_req_friendly_name", friendlyName)
	form.Set("variables", string(varsJSON))
	form.Set("doc_id", docID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: building request: %v", ErrRequestFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.setRequestHeaders(req, friendlyName, lsd)
	req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRequestFailed, err)
	}
	defer resp.Body.Close()

	c.updateRateLimit(resp.Header)
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrSessionExpired
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrForbidden
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		wait := parseRetryAfter(resp.Header.Get("Retry-After"), 60*time.Second)
		c.rlMu.Lock()
		c.rlState.Remaining = 0
		c.rlState.RetryAfter = wait
		if c.rlState.Reset.IsZero() || time.Until(c.rlState.Reset) < wait {
			c.rlState.Reset = time.Now().Add(wait)
		}
		c.rlMu.Unlock()
		c.gapMu.Lock()
		if earliest := time.Now().Add(wait); c.lastReqAt.Before(earliest) {
			c.lastReqAt = earliest
		}
		c.gapMu.Unlock()
		return nil, fmt.Errorf("%w: retry after %s", ErrRateLimited, wait)
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: HTTP %d", ErrRequestFailed, resp.StatusCode)
	default:
		return nil, fmt.Errorf("%w: unexpected HTTP %d", ErrRequestFailed, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrRequestFailed, err)
	}
	body = stripFBPrefix(body)
	if c.responseObserver != nil {
		c.responseObserver(append([]byte(nil), body...))
	}
	return body, nil
}

// doGraphQL performs a single form-encoded POST to /api/graphql/.
func (c *Client) doGraphQL(ctx context.Context, friendlyName, docID string, varsJSON []byte) (json.RawMessage, error) {
	c.waitForGap(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	dtsg, lsd, rev, hs, hsi, spinT, spinS, jazoest := c.session.snap()
	req64 := strconv.FormatUint(c.reqCounter.Add(1), 16)

	form := url.Values{}
	form.Set("av", c.cookies.CUser)
	form.Set("__user", c.cookies.CUser)
	form.Set("__a", "1")
	form.Set("__req", req64)
	form.Set("__hs", hs)
	form.Set("dpr", "2")
	form.Set("__ccg", "EXCELLENT")
	form.Set("__rev", rev)
	if spinS != "" {
		form.Set("__s", spinS)
	}
	form.Set("__hsi", hsi)
	form.Set("__comet_req", "15")
	form.Set("fb_dtsg", dtsg)
	form.Set("jazoest", jazoest)
	form.Set("lsd", lsd)
	form.Set("__spin_r", rev)
	form.Set("__spin_b", "trunk")
	form.Set("__spin_t", spinT)
	form.Set("server_timestamps", "true")
	form.Set("fb_api_caller_class", "RelayModern")
	form.Set("fb_api_req_friendly_name", friendlyName)
	form.Set("variables", string(varsJSON))
	form.Set("doc_id", docID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: building request: %v", ErrRequestFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.setRequestHeaders(req, friendlyName, lsd)
	req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRequestFailed, err)
	}
	defer resp.Body.Close()

	c.updateRateLimit(resp.Header)
	switch {
	case resp.StatusCode == http.StatusOK:
		// handled below
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrSessionExpired
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrForbidden
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		wait := parseRetryAfter(resp.Header.Get("Retry-After"), 60*time.Second)
		c.rlMu.Lock()
		c.rlState.Remaining = 0
		c.rlState.RetryAfter = wait
		if c.rlState.Reset.IsZero() || time.Until(c.rlState.Reset) < wait {
			c.rlState.Reset = time.Now().Add(wait)
		}
		c.rlMu.Unlock()
		c.gapMu.Lock()
		if earliest := time.Now().Add(wait); c.lastReqAt.Before(earliest) {
			c.lastReqAt = earliest
		}
		c.gapMu.Unlock()
		return nil, fmt.Errorf("%w: retry after %s", ErrRateLimited, wait)
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: HTTP %d", ErrRequestFailed, resp.StatusCode)
	default:
		return nil, fmt.Errorf("%w: unexpected HTTP %d", ErrRequestFailed, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrRequestFailed, err)
	}

	// Strip the XSS guard prefix that Facebook prepends to every response.
	body = stripFBPrefix(body)
	if c.responseObserver != nil {
		c.responseObserver(append([]byte(nil), body...))
	}

	// Facebook returns incremental Relay payloads as newline-delimited JSON.
	// The first line contains a partial data tree; later lines carry {path,data}
	// patches (often including more feed edges and page_info). Ignoring those
	// lines silently drops posts, so rebuild the complete tree before decoding.
	lines := bytes.Split(body, []byte("\n"))
	var merged interface{}
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		line = stripFBPrefix(line)
		if len(line) == 0 {
			continue
		}

		var envelope gqlEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}

		if err := envelope.err(); err != nil {
			return nil, err
		}

		if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
			var patch interface{}
			if json.Unmarshal(envelope.Data, &patch) != nil {
				continue
			}
			if len(envelope.Path) == 0 {
				if merged == nil {
					merged = patch
				} else {
					merged = mergeJSONValue(merged, patch)
				}
			} else if merged != nil {
				merged = mergeJSONPath(merged, envelope.Path, patch)
			}
		}
	}
	if merged != nil {
		if out, err := json.Marshal(merged); err == nil {
			return out, nil
		}
	}

	return nil, fmt.Errorf("%w: no data in response (body: %s)", ErrRequestFailed, truncate(string(body), 300))
}

// graphqlAllLines is like graphql but returns ALL data payloads from a
// multi-line response. Useful for streaming queries (like search) where
// the results arrive across multiple JSON payloads.
func (c *Client) graphqlAllLines(ctx context.Context, friendlyName string, variables interface{}) ([]json.RawMessage, error) {
	docID := c.docID(friendlyName)
	if docID == "" {
		return nil, fmt.Errorf("%w: no doc_id registered for %q", ErrInvalidParams, friendlyName)
	}
	varsJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, fmt.Errorf("%w: marshalling variables: %v", ErrInvalidParams, err)
	}

	raw, err := c.doGraphQLRaw(ctx, friendlyName, docID, varsJSON)
	if err != nil {
		return nil, err
	}

	var allData []json.RawMessage
	lines := bytes.Split(raw, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		line = stripFBPrefix(line)
		if len(line) == 0 {
			continue
		}
		var envelope gqlEnvelope
		if json.Unmarshal(line, &envelope) != nil {
			continue
		}
		if err := envelope.err(); err != nil {
			return nil, err
		}
		if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
			allData = append(allData, envelope.Data)
		}
	}
	if len(allData) == 0 {
		return nil, fmt.Errorf("%w: no data in multi-line response", ErrRequestFailed)
	}
	return allData, nil
}

func mergeJSONValue(dst, patch interface{}) interface{} {
	dm, dok := dst.(map[string]interface{})
	pm, pok := patch.(map[string]interface{})
	if dok && pok {
		for k, v := range pm {
			if old, ok := dm[k]; ok {
				dm[k] = mergeJSONValue(old, v)
			} else {
				dm[k] = v
			}
		}
		return dm
	}
	return patch
}

func mergeJSONPath(root interface{}, path []interface{}, patch interface{}) interface{} {
	if len(path) == 0 {
		return mergeJSONValue(root, patch)
	}
	head := path[0]
	switch node := root.(type) {
	case map[string]interface{}:
		key, ok := head.(string)
		if !ok {
			return root
		}
		child, exists := node[key]
		if !exists {
			if len(path) > 1 {
				if _, ok := path[1].(float64); ok {
					child = []interface{}{}
				} else {
					child = map[string]interface{}{}
				}
			}
		}
		node[key] = mergeJSONPath(child, path[1:], patch)
		return node
	case []interface{}:
		idx := -1
		switch n := head.(type) {
		case float64:
			idx = int(n)
		case int:
			idx = n
		}
		if idx < 0 {
			return root
		}
		for len(node) <= idx {
			node = append(node, nil)
		}
		child := node[idx]
		if child == nil && len(path) > 1 {
			if _, ok := path[1].(float64); ok {
				child = []interface{}{}
			} else {
				child = map[string]interface{}{}
			}
		}
		node[idx] = mergeJSONPath(child, path[1:], patch)
		return node
	default:
		if _, ok := head.(float64); ok {
			return mergeJSONPath([]interface{}{}, path, patch)
		}
		return mergeJSONPath(map[string]interface{}{}, path, patch)
	}
}

// waitForGap enforces the min request gap, honouring rate-limit state adaptively.
// It reserves the next slot atomically, releases the lock, and then sleeps
// independently so concurrent callers don't serialise behind one in-flight wait.
func (c *Client) waitForGap(ctx context.Context) {
	gap := c.adaptiveGap()
	c.gapMu.Lock()
	now := time.Now()
	next := c.lastReqAt.Add(gap)
	if now.After(next) {
		next = now
	}
	c.lastReqAt = next
	c.gapMu.Unlock()

	if wait := time.Until(next); wait > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	c.rlMu.Lock()
	c.rlState.RetryAfter = 0
	c.rlMu.Unlock()
}

// stripFBPrefix removes the "for (;;);" prefix Facebook adds for XSS protection.
func stripFBPrefix(b []byte) []byte {
	prefix := []byte("for (;;);")
	if bytes.HasPrefix(b, prefix) {
		return b[len(prefix):]
	}
	return b
}

// parseRetryAfter parses the Retry-After header (seconds integer or HTTP-date).
func parseRetryAfter(val string, fallback time.Duration) time.Duration {
	if val == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(val); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return fallback
}

// updateRateLimit reads standard rate-limit headers and updates tracked state.
func (c *Client) updateRateLimit(h http.Header) {
	c.rlMu.Lock()
	defer c.rlMu.Unlock()
	if v := rlHeader(h, "Limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.rlState.Limit = n
		}
	}
	if v := rlHeader(h, "Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.rlState.Remaining = n
		}
	}
	if v := rlHeader(h, "Reset"); v != "" {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
			if ts > 1_000_000_000 {
				c.rlState.Reset = time.Unix(ts, 0)
			} else {
				c.rlState.Reset = time.Now().Add(time.Duration(ts) * time.Second)
			}
		}
	}
}

// rlHeader returns the value of a rate-limit header, checking four common prefix variants.
func rlHeader(h http.Header, suffix string) string {
	for _, p := range []string{"X-RateLimit-", "X-Rate-Limit-", "X-Ratelimit-", "RateLimit-"} {
		if v := strings.TrimSpace(h.Get(p + suffix)); v != "" {
			return v
		}
	}
	return ""
}

// adaptiveGap returns the delay before the next request based on rate-limit state.
func (c *Client) adaptiveGap() time.Duration {
	c.rlMu.Lock()
	rs := c.rlState
	c.rlMu.Unlock()

	if rs.Remaining == 0 && !rs.Reset.IsZero() {
		if d := time.Until(rs.Reset); d > 0 {
			return d + 50*time.Millisecond
		}
	}
	if rs.Remaining > 0 && !rs.Reset.IsZero() {
		if d := time.Until(rs.Reset); d > 0 {
			spread := d / time.Duration(float64(rs.Remaining)*0.9)
			if spread > c.minGap {
				return spread
			}
		}
	}
	return c.minGap
}

// isNonRetriable reports whether err should not be retried (4xx-class errors).
func isNonRetriable(err error) bool {
	return errors.Is(err, ErrInvalidAuth) ||
		errors.Is(err, ErrUnauthorized) ||
		errors.Is(err, ErrForbidden) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrInvalidParams) ||
		errors.Is(err, ErrDocIDStale) ||
		errors.Is(err, ErrAlreadyMember) ||
		errors.Is(err, ErrNotMember) ||
		errors.Is(err, ErrSessionExpired)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// mutationID generates a simple client mutation ID from the current time in nanoseconds.
func mutationID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 16)
}
