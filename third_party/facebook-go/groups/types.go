package groups

import "time"

// RateLimitState captures rate-limit information from the most recently observed
// response headers. All fields are zero-valued until a response with rate-limit
// headers is received.
type RateLimitState struct {
	Limit      int           `json:"limit"`
	Remaining  int           `json:"remaining"`
	Reset      time.Time     `json:"reset"`
	RetryAfter time.Duration `json:"retry_after"`
}

// IsLimited reports whether the current state indicates requests are blocked.
func (r RateLimitState) IsLimited() bool {
	if !r.Reset.IsZero() && r.Remaining == 0 && time.Now().Before(r.Reset) {
		return true
	}
	return r.RetryAfter > 0
}

// ResetIn returns how long until the rate-limit window resets.
// Returns 0 if Reset is in the past or not set.
func (r RateLimitState) ResetIn() time.Duration {
	if r.Reset.IsZero() {
		return 0
	}
	if d := time.Until(r.Reset); d > 0 {
		return d
	}
	return 0
}

// Cookies holds the Facebook session cookies obtained from a browser export.
// All six fields are required for full write access. xs and CUser are the
// most critical — the others strengthen fingerprint matching.
type Cookies struct {
	SB    string // sb: browser session fingerprint
	DATR  string // datr: device auth token
	CUser string // c_user: the authenticated user ID (also used as __user in every request)
	XS    string // xs: session token — the primary auth credential
	FR    string // fr: friend-request + ad-tracking token
	PSL   string // ps_l: presence/status
	PSN   string // ps_n: presence/status (secondary)
}

// Privacy is the visibility level of a Facebook Group.
type Privacy string

const (
	PrivacyPublic Privacy = "PUBLIC"
	PrivacyClosed Privacy = "CLOSED"
	PrivacySecret Privacy = "SECRET"
)

// Group is the canonical group metadata model.
type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Description string    `json:"description,omitempty"`
	MemberCount int       `json:"memberCount,omitempty"`
	Privacy     Privacy   `json:"privacy"`
	CoverURL    string    `json:"coverUrl,omitempty"`
	AdminIDs    []string  `json:"adminIds,omitempty"`
	Joined      bool      `json:"joined"`
	PendingJoin bool      `json:"pendingJoin"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
}

// Post represents a group post / story node.
// FeedbackID is required for comment and reaction mutations — it differs from ID.
type Post struct {
	ID            string    `json:"id"`
	FeedbackID    string    `json:"feedbackId"`
	GroupID       string    `json:"groupId,omitempty"`
	AuthorID      string    `json:"authorId,omitempty"`
	AuthorName    string    `json:"authorName,omitempty"`
	Message       string    `json:"message,omitempty"`
	Attachments   []string  `json:"attachments,omitempty"`
	ReactionCount int       `json:"reactionCount"`
	CommentCount  int       `json:"commentCount"`
	ShareCount    int       `json:"shareCount"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt,omitempty"`
}

// Comment represents a comment on a post.
type Comment struct {
	ID            string    `json:"id"`
	FeedbackID    string    `json:"feedbackId,omitempty"`
	AuthorID      string    `json:"authorId,omitempty"`
	AuthorName    string    `json:"authorName,omitempty"`
	Message       string    `json:"message,omitempty"`
	ReactionCount int       `json:"reactionCount"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Member is a group member.
type Member struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	IsAdmin bool   `json:"isAdmin"`
}

// GroupSearchResult is a lightweight result from SearchGroups or DiscoverGroups.
type GroupSearchResult struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	URL         string  `json:"url,omitempty"`
	MemberCount int     `json:"memberCount,omitempty"`
	Privacy     Privacy `json:"privacy,omitempty"`
	CoverURL    string  `json:"coverUrl,omitempty"`
}

// FeedPage is one page of group feed posts with a cursor for the next page.
type FeedPage struct {
	Posts      []Post `json:"posts"`
	NextCursor string `json:"nextCursor,omitempty"`
	HasNext    bool   `json:"hasNext"`
}

// CommentPage is one page of post comments with a cursor for the next page.
type CommentPage struct {
	Comments   []Comment `json:"comments"`
	NextCursor string    `json:"nextCursor,omitempty"`
	HasNext    bool      `json:"hasNext"`
}

// MemberPage is one page of group members with a cursor for the next page.
type MemberPage struct {
	Members    []Member `json:"members"`
	NextCursor string   `json:"nextCursor,omitempty"`
	HasNext    bool     `json:"hasNext"`
}

// ReactionType enumerates the seven Facebook post reactions.
type ReactionType string

const (
	ReactionLike  ReactionType = "LIKE"
	ReactionLove  ReactionType = "LOVE"
	ReactionCare  ReactionType = "CARE"
	ReactionHaha  ReactionType = "HAHA"
	ReactionWow   ReactionType = "WOW"
	ReactionSad   ReactionType = "SAD"
	ReactionAngry ReactionType = "ANGRY"
)

// CreateGroupParams holds the inputs for CreateGroup.
type CreateGroupParams struct {
	Name        string
	Privacy     Privacy // defaults to PrivacyClosed if zero
	Description string
}

// MembershipQuestion represents a single question that a closed group asks
// prospective members. Some groups require all questions answered before the
// join request is accepted.
type MembershipQuestion struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Type     string   `json:"type"` // OPEN_ENDED, MULTIPLE_CHOICE
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"` // populated for MULTIPLE_CHOICE
}

// MembershipAnswer is one answer to a [MembershipQuestion]. For open-ended
// questions, set Text. For multiple-choice questions, set Choice to one of
// the question's Options.
type MembershipAnswer struct {
	QuestionID string `json:"questionId"`
	Text       string `json:"text,omitempty"`
	Choice     string `json:"choice,omitempty"`
}

// TrendReport is the output of ScrapeGroupTrends.
type TrendReport struct {
	GroupID        string           `json:"groupId"`
	PostsAnalyzed  int              `json:"postsAnalyzed"`
	TopKeywords    []KeywordFreq    `json:"topKeywords"`
	TopHashtags    []KeywordFreq    `json:"topHashtags"`
	AvgEngagement  float64          `json:"avgEngagement"`
	PeakHours      []int            `json:"peakHours"`
	SentimentScore float64          `json:"sentimentScore"`
	ActiveAuthors  []AuthorActivity `json:"activeAuthors"`
}

// KeywordFreq pairs a term with its occurrence count.
type KeywordFreq struct {
	Term  string `json:"term"`
	Count int    `json:"count"`
}

// AuthorActivity summarises one author's posting activity within the analysis window.
type AuthorActivity struct {
	AuthorID   string `json:"authorId"`
	AuthorName string `json:"authorName"`
	PostCount  int    `json:"postCount"`
}
