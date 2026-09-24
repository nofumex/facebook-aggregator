package facebook

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/teslashibe/facebook-go/groups"
)

type TeslaShibe struct{ client *groups.Client }

type TeslaShibeConfig struct {
	Cookies          groups.Cookies
	MinRequestGap    time.Duration
	MaxRetries       int
	DocIDs           map[string]string
	ResponseObserver func([]byte)
	DisableHTTP2     bool
}

func NewTeslaShibe(c TeslaShibeConfig) (*TeslaShibe, error) {
	opts := []groups.Option{groups.WithMinRequestGap(c.MinRequestGap), groups.WithRetry(c.MaxRetries, time.Second), groups.WithDocIDs(c.DocIDs), groups.WithResponseObserver(c.ResponseObserver)}
	if c.DisableHTTP2 {
		opts = append(opts, groups.WithHTTPClient(facebookHTTP1Client()))
	}
	client, err := groups.New(c.Cookies, opts...)
	if err != nil {
		return nil, classify(err)
	}
	return &TeslaShibe{client: client}, nil
}

func facebookHTTP1Client() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}
}

func (a *TeslaShibe) Name() string { return "teslashibe/facebook-go (adapted)" }

func (a *TeslaShibe) ResolveGroup(ctx context.Context, idOrSlug string) (string, string, string, error) {
	ref := groupRef(idOrSlug)
	if ref == "" {
		return "", "", "", fmt.Errorf("invalid Facebook group URL")
	}
	g, err := a.client.GetGroup(ctx, ref)
	if err == nil {
		url := g.URL
		if url == "" {
			url = "https://www.facebook.com/groups/" + ref + "/"
		}
		return g.ID, g.Name, url, nil
	}
	// Slugs are not accepted by every version of CometGroupRootQuery. Search is
	// a safe fallback and exact URL/slug matching avoids selecting another group.
	if !digits.MatchString(ref) {
		items, searchErr := a.client.SearchGroups(ctx, strings.ReplaceAll(ref, "-", " "), groups.WithSearchLimit(20))
		if searchErr == nil {
			for _, item := range items {
				if groupRef(item.URL) == ref {
					return item.ID, item.Name, item.URL, nil
				}
			}
		}
	}
	return "", "", "", classify(err)
}

func (a *TeslaShibe) Check(ctx context.Context, groupID string) error {
	_, err := a.client.GetGroupPosts(ctx, groupID)
	return classify(err)
}

func (a *TeslaShibe) FetchRecent(ctx context.Context, req FetchRequest) (FetchResult, error) {
	if req.MaxPages < 1 {
		req.MaxPages = 8
	}
	stopBefore := req.StopBefore
	if !stopBefore.IsZero() && req.Overlap > 0 {
		stopBefore = stopBefore.Add(-req.Overlap)
	}
	var out FetchResult
	var page groups.FeedPage
	var err error
	for n := 0; n < req.MaxPages; n++ {
		if n == 0 {
			page, err = a.client.GetGroupPosts(ctx, req.GroupID)
		} else {
			page, err = a.client.GetGroupPostsPage(ctx, req.GroupID, page.NextCursor)
		}
		if err != nil {
			return out, classify(err)
		}
		for _, p := range page.Posts {
			if p.ID == req.StopPostID || (!stopBefore.IsZero() && !p.CreatedAt.IsZero() && p.CreatedAt.Before(stopBefore)) {
				out.ReachedOld = true
				continue
			}
			raw, _ := json.Marshal(p)
			post := domain.FacebookPost{ID: p.ID, GroupID: req.GroupID, URL: postURL(req.GroupID, p.ID), AuthorID: p.AuthorID, AuthorName: p.AuthorName, Text: p.Message, PublishedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, MediaURLs: imageURLs(p.Attachments), Raw: raw}
			out.Posts = append(out.Posts, post)
			if out.NewestAt.IsZero() || p.CreatedAt.After(out.NewestAt) {
				out.NewestAt, out.NewestID = p.CreatedAt, p.ID
			}
			if req.MaxPosts > 0 && len(out.Posts) >= req.MaxPosts {
				return out, nil
			}
		}
		if out.ReachedOld || !page.HasNext || page.NextCursor == "" {
			break
		}
	}
	return out, nil
}

// The upstream attachment tree also contains post, profile and outbound-link
// URLs. Persist only actual image resources so Telegram never tries to render a
// Facebook page or a video as a photo.
func imageURLs(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			continue
		}
		host, path := strings.ToLower(u.Hostname()), strings.ToLower(u.Path)
		valid := strings.Contains(host, "scontent") || strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") || strings.HasSuffix(path, ".png") || strings.HasSuffix(path, ".webp")
		if valid && !seen[raw] {
			seen[raw] = true
			out = append(out, raw)
		}
	}
	return out
}

var digits = regexp.MustCompile(`^\d+$`)

func groupRef(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, "/"))
	if i := strings.Index(s, "facebook.com/groups/"); i >= 0 {
		s = s[i+len("facebook.com/groups/"):]
		if j := strings.IndexByte(s, '/'); j >= 0 {
			s = s[:j]
		}
		if j := strings.IndexByte(s, '?'); j >= 0 {
			s = s[:j]
		}
	}
	return strings.TrimSpace(s)
}
func postURL(groupID, postID string) string {
	id := postID
	if p := strings.LastIndex(id, "_"); p >= 0 {
		id = id[p+1:]
	}
	return fmt.Sprintf("https://www.facebook.com/groups/%s/posts/%s/", groupID, id)
}
func classify(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, groups.ErrInvalidAuth), errors.Is(err, groups.ErrUnauthorized), errors.Is(err, groups.ErrSessionExpired):
		return fmt.Errorf("%w: %v", ErrAuthentication, err)
	case strings.Contains(message, "1357001"), strings.Contains(message, "log in to continue"), strings.Contains(message, "not logged in"), strings.Contains(message, "please log in"), strings.Contains(message, "login required"), strings.Contains(message, "session expired"), strings.Contains(message, "invalid session"), strings.Contains(message, "authentication failed"), strings.Contains(message, "oauthexception"):
		return fmt.Errorf("%w: %v", ErrAuthentication, err)
	case errors.Is(err, groups.ErrRateLimited):
		return fmt.Errorf("%w: %v", ErrRateLimited, err)
	case errors.Is(err, groups.ErrForbidden):
		return fmt.Errorf("%w: %v", ErrForbidden, err)
	case errors.Is(err, groups.ErrDocIDStale):
		return fmt.Errorf("%w: %v", ErrProtocolChanged, err)
	default:
		return err
	}
}
