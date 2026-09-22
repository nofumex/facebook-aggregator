package groups

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultUserAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	defaultMaxRetries = 3
	defaultRetryBase  = 500 * time.Millisecond
	defaultMinGap     = 800 * time.Millisecond
	graphqlEndpoint   = "https://www.facebook.com/api/graphql/"
	referer           = "https://www.facebook.com/"
	origin            = "https://www.facebook.com"
	asbdID            = "129477"
)

// Default doc_ids keyed by the Facebook-friendly query name.
// These rotate with each code deploy; override via WithDocIDs.
// Default doc_ids harvested from live Facebook JS bundles (2026-04-20).
// These rotate with each code deploy; override via WithDocIDs.
var defaultDocIDs = map[string]string{
	// Groups — core
	"GroupsCometCrossGroupFeedContainerQuery":            "26735348219462271",
	"GroupsCometCrossGroupFeedPaginationQuery":           "26527780253581396",
	"GroupsCometLeftRailContainerQuery":                  "31152611061018930",
	"GroupsCometJoinsRootQuery":                          "24648931168042404",
	"GroupsCometDiscoverContentQuery":                    "25947833531582461",
	"GroupsCometCreateRootQuery":                         "26545572188428154",
	"GroupsCometSettingMenuQuery":                        "9746765062081053",
	"GroupsCometMoreActionMenuQuery":                     "26476435205301604",
	"GroupsCometEntityMenuEmbeddedRootQuery":             "26566099453080408",
	"GroupsCometRecAffordanceSectionQuery":               "26227921180209081",
	"GroupsCometGroupRuleEntityDialogQuery":              "25942555452109877",
	"GroupsCometHeaderInviteMenuQuery":                   "34670067305939872",
	"GroupsCometMembershipQuestionsPreloadedDialogQuery": "26323827953971452",

	// Groups — pagination & joined
	"GroupsCometAllJoinedGroupsSectionPaginationQuery":        "9974006939348139",
	"GroupsCometPendingGroupJoinsSectionPaginationQuery":      "9924911067556167",
	"GroupsCometCategoriesSectionCategoriesRefetchQuery":      "26179397388389630",
	"GroupsCometCategoriesSectionMoreSuggestionsRefetchQuery": "24774907385540983",
	"GroupsLeftRailYourGroupsPaginatedQuery":                  "9658982227546884",
	"GroupsLeftRailGroupsYouManagePaginatedQuery":             "10000015690112057",

	// Feed & posts
	"CometModernHomeFeedQuery":          "35430709819853581",
	"CometSinglePostDialogContentQuery": "26485835561067485",
	"CometFeedStoryMenuQuery":           "26578139061874750",
	"CometFeedInlineComposerQuery":      "26472956975672575",
	"ProfileCometComposerRootQuery":     "26292641347064220",
	"ProfileCometTimelineFeedQuery":     "26484140144542166",

	// Comments & reactions queries (mutations are below)
	"CometUFICommentRefetchQuery":  "26577964715169700",
	"CometUFICommentMenuQuery":     "26230165949984969",
	"CometUFIReactionsDialogQuery": "33437545572555426",
	// Alias used by GetPostComments
	"CommentsListComponentPaginationQuery": "26577964715169700",

	// Groups — utility
	"GroupsCometUFIAnonActorSwitcherMenuQuery": "25736011312767229",

	// Search
	"CometSearchKeywordDataSourceQuery":     "34279758474973265",
	"SearchCometResultsInitialResultsQuery": "35435103342769591",

	// Group detail & members
	"GroupsCometDiscussionLayoutRootQuery": "26595005356798382",
	"GroupsCometDiscussionRootQuery":       "26867841739523736",
	"GroupsCometMembersRootQuery":          "26328488663483009",
	"CometGroupRootQuery":                  "26675395318752468",

	// Membership questions (gated groups)
	"GroupsCometParticipationQuestionsDialogQuery":     "27243898798531316",
	"GroupsCometRequestToParticipateOnJoinDialogQuery": "27505387249060985",

	// Single-group feed (posts from one specific group)
	"CometGroupDiscussionRootSuccessQuery": "26867841739523736",

	// Mutations — groups (verified live via JS bundle harvest 2026-04-20)
	"useGroupRequestToJoinMutation":  "25864060869939410",
	"useGroupLeaveMutation":          "34892951610348516",
	"useGroupsCometCreateMutation":   "26410639795238208",
	"useGroupsCometFollowMutation":   "23954755464142194",
	"useGroupsCometUnfollowMutation": "9558102717592331",
	"useGroupsCometVisitMutation":    "26506406622322884",
	"useGroupHideSuggestionMutation": "9454440231347992",

	// Mutations — posts & comments (verified live)
	"GroupCometComposerCreateDialogQuery":          "27499428346323949",
	"GroupsCometInlineComposerRendererUpdateQuery": "26422734134060146",
	"ComposerStoryCreateMutation":                  "25879074401770690",
	"ComposerStoryEditMutation":                    "26629276700039334",
	"useCometUFICreateCommentMutation":             "26608664635429703",
	"CometUFIFeedbackReactMutation":                "34892649590379796",
	"ReelComposerReelPublishMutation":              "24143317328587698",
	"VideoComposerVideoPublishingMutation":         "29705123342436060",

	// Feed pagination (group-specific)
	"GroupsCometFeedRegularStoriesPaginationQuery":  "26577462205242925",
	"GroupsCometFeedHoistedStoriesnPaginationQuery": "34927333560246608",
}

// Client is a Facebook Groups API client. It is safe for concurrent use.
type Client struct {
	cookies    Cookies
	httpClient *http.Client
	userAgent  string
	docIDs     map[string]string
	maxRetries int
	retryBase  time.Duration
	minGap     time.Duration

	reqCounter atomic.Uint64
	session    *sessionState

	// gapMu + lastReqAt implement the leaky-bucket rate limiter.
	gapMu     sync.Mutex
	lastReqAt time.Time

	// rlMu + rlState track the most recently observed rate-limit headers.
	rlMu             sync.Mutex
	rlState          RateLimitState
	responseObserver func([]byte)
}

// New constructs a Client and performs the session bootstrap immediately.
// It returns ErrInvalidAuth if Cookies.CUser or Cookies.XS is empty.
// It returns ErrUnauthorized if the bootstrap page cannot supply valid tokens.
func New(cookies Cookies, opts ...Option) (*Client, error) {
	if cookies.CUser == "" || cookies.XS == "" {
		return nil, fmt.Errorf("%w: CUser and XS must both be non-empty", ErrInvalidAuth)
	}

	docIDs := make(map[string]string, len(defaultDocIDs))
	for k, v := range defaultDocIDs {
		docIDs[k] = v
	}

	c := &Client{
		cookies:    cookies,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		userAgent:  defaultUserAgent,
		docIDs:     docIDs,
		maxRetries: defaultMaxRetries,
		retryBase:  defaultRetryBase,
		minGap:     defaultMinGap,
		session:    &sessionState{},
	}

	for _, o := range opts {
		o(c)
	}

	if err := c.bootstrap(); err != nil {
		return nil, err
	}
	return c, nil
}

// Option configures a Client.
type Option func(*Client)

// WithUserAgent overrides the default Chrome User-Agent string.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// WithDocIDs overrides one or more GraphQL doc_ids. The key is the Facebook
// friendly query name (e.g. "GroupsCometGroupFeedQuery").
func WithDocIDs(overrides map[string]string) Option {
	return func(c *Client) {
		for k, v := range overrides {
			c.docIDs[k] = v
		}
	}
}

// WithRetry configures retry behaviour.
// Default: 3 attempts, 500ms exponential base (500ms → 1s → 2s).
// Set maxAttempts to 0 or 1 to disable retries.
func WithRetry(maxAttempts int, base time.Duration) Option {
	return func(c *Client) {
		c.maxRetries = maxAttempts
		c.retryBase = base
	}
}

// WithHTTPClient replaces the default http.Client. Nil is ignored.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithProxy routes all HTTP traffic through the given proxy URL
// (e.g. "http://user:pass@host:port").
func WithProxy(proxyURL string) Option {
	return func(c *Client) {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(parsed)
		c.httpClient = &http.Client{
			Timeout:   c.httpClient.Timeout,
			Transport: transport,
		}
	}
}

// WithMinRequestGap sets the minimum time between consecutive requests.
// Default: 800ms. Lower values risk triggering Facebook's rate limiter.
func WithMinRequestGap(d time.Duration) Option {
	return func(c *Client) { c.minGap = d }
}

// WithResponseObserver observes a copy of raw GraphQL response bytes. It is
// intended for protocol-shape diagnostics and must not log response contents.
func WithResponseObserver(fn func([]byte)) Option {
	return func(c *Client) { c.responseObserver = fn }
}

// RateLimit returns a snapshot of the most recently observed rate-limit state.
func (c *Client) RateLimit() RateLimitState {
	c.rlMu.Lock()
	defer c.rlMu.Unlock()
	return c.rlState
}

// docID returns the doc_id for the given friendly name.
func (c *Client) docID(name string) string {
	if id, ok := c.docIDs[name]; ok {
		return id
	}
	return ""
}

// cookieHeader builds the Cookie header value from the Cookies struct.
func (c *Client) cookieHeader() string {
	jar := ""
	add := func(name, val string) {
		if val == "" {
			return
		}
		if jar != "" {
			jar += "; "
		}
		jar += name + "=" + val
	}
	add("sb", c.cookies.SB)
	add("datr", c.cookies.DATR)
	add("c_user", c.cookies.CUser)
	add("xs", c.cookies.XS)
	add("fr", c.cookies.FR)
	add("ps_l", c.cookies.PSL)
	add("ps_n", c.cookies.PSN)
	return jar
}

// setRequestHeaders sets the common headers (User-Agent, cookies, etc.) on req.
// friendlyName and lsd are optional and only used for GraphQL POST requests.
func (c *Client) setRequestHeaders(req *http.Request, friendlyName, lsd string) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", referer)
	req.Header.Set("Origin", origin)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Cookie", c.cookieHeader())
	if friendlyName != "" {
		req.Header.Set("X-FB-Friendly-Name", friendlyName)
	}
	if lsd != "" {
		req.Header.Set("X-FB-LSD", lsd)
	}
	if friendlyName != "" || lsd != "" {
		req.Header.Set("X-ASBD-ID", asbdID)
	}
}

// SearchOption configures SearchGroups.
type SearchOption func(*searchOptions)

type searchOptions struct {
	location string
	limit    int
}

// WithSearchLocation filters group search results by city or region name.
func WithSearchLocation(cityOrRegion string) SearchOption {
	return func(o *searchOptions) { o.location = cityOrRegion }
}

// WithSearchLimit caps the number of groups returned. Default: 20.
func WithSearchLimit(n int) SearchOption {
	return func(o *searchOptions) { o.limit = n }
}

// PostOption configures CreatePost.
type PostOption func(*postOptions)

type postOptions struct {
	attachmentURL string
}

// WithPostAttachmentURL appends a link attachment to the created post.
func WithPostAttachmentURL(rawURL string) PostOption {
	return func(o *postOptions) { o.attachmentURL = rawURL }
}

// TrendOption configures ScrapeGroupTrends.
type TrendOption func(*trendOptions)

type trendOptions struct {
	maxPosts  int
	topN      int
	stopWords []string
}

// WithTrendMaxPosts caps the total posts fetched for trend analysis. Default: 200.
func WithTrendMaxPosts(n int) TrendOption {
	return func(o *trendOptions) { o.maxPosts = n }
}

// WithTrendTopN sets the number of top keywords and hashtags returned. Default: 20.
func WithTrendTopN(n int) TrendOption {
	return func(o *trendOptions) { o.topN = n }
}

// WithTrendStopWords appends domain-specific stop words to the bundled English
// stop list before keyword extraction.
func WithTrendStopWords(words []string) TrendOption {
	return func(o *trendOptions) { o.stopWords = append(o.stopWords, words...) }
}
