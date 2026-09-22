package groups

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// GraphQL envelope
// ---------------------------------------------------------------------------

// gqlEnvelope is the top-level shape returned by every /api/graphql/ call.
// Facebook wraps all responses with "for (;;);" (stripped by client.go) and
// the body is always JSON matching this shape.
type gqlEnvelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
	Path   []interface{}   `json:"path"`
}

type gqlError struct {
	Message string `json:"message"`
	// api_error_code 1357001 = not logged in, 368 = blocked, etc.
	Code     int    `json:"api_error_code"`
	Severity string `json:"severity"`
	// type is set to "OAuthException" for auth failures.
	Type string `json:"type"`
}

// err converts the error list into a Go error, mapping known codes to
// the package sentinels.
//
// Facebook frequently returns "WARNING" severity errors alongside successful
// data (e.g. missing relay provider variables that don't actually break the
// query). We only treat CRITICAL and unspecified-severity errors as failures.
// Callers will still receive the data when only warnings are present.
func (e *gqlEnvelope) err() error {
	if len(e.Errors) == 0 {
		return nil
	}
	// Find the first non-WARNING error. If all errors are warnings AND data
	// is present, treat the response as a success.
	var first gqlError
	hasNonWarning := false
	for _, err := range e.Errors {
		if !strings.EqualFold(err.Severity, "WARNING") {
			first = err
			hasNonWarning = true
			break
		}
	}
	if !hasNonWarning {
		// All warnings — only treat as error if there's no data.
		if len(e.Data) > 0 && string(e.Data) != "null" {
			return nil
		}
		first = e.Errors[0]
	}
	msg := strings.ToLower(first.Message)

	switch {
	case first.Code == 1357001 || first.Type == "OAuthException" ||
		strings.Contains(msg, "not logged in") || strings.Contains(msg, "session"):
		return ErrSessionExpired

	case strings.Contains(msg, "already a member"), strings.Contains(msg, "already joined"):
		return ErrAlreadyMember

	case strings.Contains(msg, "not a member"), strings.Contains(msg, "not member"):
		return ErrNotMember

	case strings.Contains(msg, "does not exist"), strings.Contains(msg, "deleted"),
		strings.Contains(msg, "not found"):
		if strings.Contains(msg, "document") || strings.Contains(msg, "doc_id") {
			return ErrDocIDStale
		}
		return ErrNotFound

	case strings.Contains(msg, "permission"), strings.Contains(msg, "forbidden"),
		strings.Contains(msg, "not allowed"), strings.Contains(msg, "access denied"):
		return ErrForbidden
	}

	return fmt.Errorf("%w: %s (code %d)", ErrRequestFailed, first.Message, first.Code)
}

// ---------------------------------------------------------------------------
// Shared sub-shapes
// ---------------------------------------------------------------------------

type fbPageInfo struct {
	HasNextPage bool   `json:"has_next_page"`
	EndCursor   string `json:"end_cursor"`
}

type fbText struct {
	Text string `json:"text"`
}

type fbImage struct {
	URI string `json:"uri"`
}

type fbActor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type fbPrivacyInfo struct {
	PrivacyType string `json:"privacy_type"`
}

type fbReactionCount struct {
	Count int `json:"count"`
}

type fbCommentCount struct {
	TotalCount int `json:"total_count"`
}

type fbShareCount struct {
	Count int `json:"count"`
}

// fbGroup is the common group representation across multiple responses.
type fbGroup struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	URL         string        `json:"url"`
	Description *fbText       `json:"description"`
	MemberCount int           `json:"member_count"`
	PrivacyInfo fbPrivacyInfo `json:"privacy_info"`
	CoverPhoto  *fbCoverPhoto `json:"cover_photo"`
	// viewer_actor_membership_status: MEMBER | PENDING | NOT_MEMBER
	ViewerMembershipStatus string `json:"viewer_actor_membership_status"`
	// Creation time as unix seconds.
	CreationTime int64 `json:"creation_time"`
}

type fbCoverPhoto struct {
	Photo *struct {
		Image *fbImage `json:"image"`
	} `json:"photo"`
}

func (g *fbGroup) toGroup() Group {
	out := Group{
		ID:          g.ID,
		Name:        g.Name,
		URL:         g.URL,
		MemberCount: g.MemberCount,
		Privacy:     Privacy(strings.ToUpper(g.PrivacyInfo.PrivacyType)),
	}
	if g.Description != nil {
		out.Description = g.Description.Text
	}
	if g.CoverPhoto != nil && g.CoverPhoto.Photo != nil && g.CoverPhoto.Photo.Image != nil {
		out.CoverURL = g.CoverPhoto.Photo.Image.URI
	}
	if g.CreationTime > 0 {
		out.CreatedAt = time.Unix(g.CreationTime, 0).UTC()
	}
	switch g.ViewerMembershipStatus {
	case "MEMBER":
		out.Joined = true
	case "PENDING":
		out.PendingJoin = true
	}
	return out
}

// fbGroupSearchResult is the lightweight shape used in search / discover lists.
type fbGroupSearchResult struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	URL            string        `json:"url"`
	MemberCount    int           `json:"member_count"`
	PrivacyInfo    fbPrivacyInfo `json:"privacy_info"`
	ProfilePicture *struct {
		URI string `json:"uri"`
	} `json:"profile_picture"`
}

func (g *fbGroupSearchResult) toSearchResult() GroupSearchResult {
	out := GroupSearchResult{
		ID:          g.ID,
		Name:        g.Name,
		URL:         g.URL,
		MemberCount: g.MemberCount,
		Privacy:     Privacy(strings.ToUpper(g.PrivacyInfo.PrivacyType)),
	}
	if g.ProfilePicture != nil {
		out.CoverURL = g.ProfilePicture.URI
	}
	return out
}

// fbStory represents a single news feed story (post).
type fbStory struct {
	ID           string      `json:"id"`
	PostID       string      `json:"post_id"`
	Feedback     *fbFeedback `json:"feedback"`
	Actors       []fbActor   `json:"actors"`
	Message      *fbText     `json:"message"`
	CreationTime int64       `json:"creation_time"`
	EditedTime   int64       `json:"edited_time"`
	// Attachments contain URLs for linked content.
	Attachments []fbAttachment `json:"attachments"`
}

type fbFeedback struct {
	ID              string           `json:"id"`
	ReactionCount   *fbReactionCount `json:"reaction_count"`
	CommentCount    *fbCommentCount  `json:"comment_count"`
	ShareCount      *fbShareCount    `json:"share_count"`
	OwningProfile   *fbActor         `json:"owning_profile"`
	AssociatedGroup *struct {
		ID string `json:"id"`
	} `json:"associated_group"`
}

type fbAttachment struct {
	Media *struct {
		PlayableURL string   `json:"playable_url"`
		Image       *fbImage `json:"image"`
	} `json:"media"`
	URL  string   `json:"url"`
	URLs []string `json:"-"`
}

func (a *fbAttachment) UnmarshalJSON(data []byte) error {
	type plain fbAttachment
	var base plain
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	*a = fbAttachment(base)
	var value any
	if json.Unmarshal(data, &value) == nil {
		seen := map[string]bool{}
		var visit func(any)
		visit = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, child := range x {
					if str, ok := child.(string); ok && (k == "uri" || k == "url" || k == "playable_url") && strings.HasPrefix(str, "http") && !seen[str] {
						seen[str] = true
						a.URLs = append(a.URLs, str)
					}
					visit(child)
				}
			case []any:
				for _, child := range x {
					visit(child)
				}
			}
		}
		visit(value)
	}
	return nil
}

func (s *fbStory) toPost(groupID string) Post {
	p := Post{
		ID:      s.ID,
		GroupID: groupID,
	}
	if s.Feedback != nil {
		p.FeedbackID = s.Feedback.ID
		if s.Feedback.ReactionCount != nil {
			p.ReactionCount = s.Feedback.ReactionCount.Count
		}
		if s.Feedback.CommentCount != nil {
			p.CommentCount = s.Feedback.CommentCount.TotalCount
		}
		if s.Feedback.ShareCount != nil {
			p.ShareCount = s.Feedback.ShareCount.Count
		}
	}
	if len(s.Actors) > 0 {
		p.AuthorID = s.Actors[0].ID
		p.AuthorName = s.Actors[0].Name
	}
	if s.Message != nil {
		p.Message = s.Message.Text
	}
	if s.CreationTime > 0 {
		p.CreatedAt = time.Unix(s.CreationTime, 0).UTC()
	}
	if s.EditedTime > 0 {
		p.UpdatedAt = time.Unix(s.EditedTime, 0).UTC()
	}
	for _, a := range s.Attachments {
		if len(a.URLs) > 0 {
			p.Attachments = append(p.Attachments, a.URLs...)
		} else if a.URL != "" {
			p.Attachments = append(p.Attachments, a.URL)
		} else if a.Media != nil && a.Media.Image != nil {
			p.Attachments = append(p.Attachments, a.Media.Image.URI)
		}
	}
	return p
}

// fbComment represents a single comment node.
type fbComment struct {
	ID          string      `json:"id"`
	Body        *fbText     `json:"body"`
	Author      *fbActor    `json:"author"`
	CreatedTime int64       `json:"created_time"`
	Feedback    *fbFeedback `json:"feedback"`
}

func (fc *fbComment) toComment() Comment {
	c := Comment{ID: fc.ID}
	if fc.Body != nil {
		c.Message = fc.Body.Text
	}
	if fc.Author != nil {
		c.AuthorID = fc.Author.ID
		c.AuthorName = fc.Author.Name
	}
	if fc.CreatedTime > 0 {
		c.CreatedAt = time.Unix(fc.CreatedTime, 0).UTC()
	}
	if fc.Feedback != nil {
		c.FeedbackID = fc.Feedback.ID
		if fc.Feedback.ReactionCount != nil {
			c.ReactionCount = fc.Feedback.ReactionCount.Count
		}
	}
	return c
}

// ---------------------------------------------------------------------------
// Per-query data shapes
// ---------------------------------------------------------------------------

// --- Participation/membership questions ---

type participationQuestionsData struct {
	Group *struct {
		ID                              string       `json:"id"`
		Name                            string       `json:"name"`
		CommunityParticipationQuestions []fbQuestion `json:"community_participation_questions"`
		MembershipQuestions             []fbQuestion `json:"membership_questions"`
		ViewerForumJoinState            string       `json:"viewer_forum_join_state"`
		ProfilePicture                  *struct {
			URI string `json:"uri"`
		} `json:"profile_picture"`
		GroupSnippets *struct {
			Edges []struct {
				Node *struct {
					Title *fbText `json:"title"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"group_snippets"`
	} `json:"group"`
}

type fbQuestion struct {
	ID       string  `json:"id"`
	Question *fbText `json:"question_title"`
	// Some responses use "title" or "text" directly
	TitleText string `json:"title"`
	BodyText  string `json:"text"`
	Type      string `json:"question_type"` // OPEN_ENDED | MULTIPLE_CHOICE
	Required  bool   `json:"is_required"`
	Choices   []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Text  string `json:"text"`
	} `json:"answer_choices"`
}

func (d *participationQuestionsData) toQuestions() []MembershipQuestion {
	if d.Group == nil {
		return nil
	}
	all := append([]fbQuestion{}, d.Group.CommunityParticipationQuestions...)
	all = append(all, d.Group.MembershipQuestions...)

	out := make([]MembershipQuestion, 0, len(all))
	for _, q := range all {
		mq := MembershipQuestion{
			ID:       q.ID,
			Type:     q.Type,
			Required: q.Required,
		}
		switch {
		case q.Question != nil && q.Question.Text != "":
			mq.Text = q.Question.Text
		case q.TitleText != "":
			mq.Text = q.TitleText
		case q.BodyText != "":
			mq.Text = q.BodyText
		}
		for _, c := range q.Choices {
			label := c.Label
			if label == "" {
				label = c.Text
			}
			if label != "" {
				mq.Options = append(mq.Options, label)
			}
		}
		out = append(out, mq)
	}
	return out
}

// --- Single-group feed ---
// Response: group.group_feed.edges[].node (may be Story or section header)

type singleFeedData struct {
	Group *singleFeedGroup `json:"group"`
	Node  *singleFeedGroup `json:"node"`
}
type singleFeedGroup struct {
	ID        string `json:"id"`
	GroupFeed *struct {
		Edges    []singleFeedEdge `json:"edges"`
		PageInfo *fbPageInfo      `json:"page_info"`
	} `json:"group_feed"`
}

type singleFeedEdge struct {
	Node *singleFeedNode `json:"node"`
}

type singleFeedNode struct {
	Typename      string         `json:"__typename"`
	ID            string         `json:"id"`
	PostID        string         `json:"post_id"`
	Feedback      *fbFeedback    `json:"feedback"`
	Message       *fbText        `json:"message"`
	CreationTime  int64          `json:"creation_time"`
	EditedTime    int64          `json:"edited_time"`
	Actors        []fbActor      `json:"actors"`
	Attachments   []fbAttachment `json:"attachments"`
	CometSections *struct {
		Content *struct {
			Story *fbStory `json:"story"`
		} `json:"content"`
		Timestamp *struct {
			Story *fbStory `json:"story"`
		} `json:"timestamp"`
	} `json:"comet_sections"`
}

func (d *singleFeedData) toFeedPage() FeedPage {
	g := d.Group
	if g == nil {
		g = d.Node
	}
	if g == nil || g.GroupFeed == nil {
		return FeedPage{}
	}
	gid := g.ID
	page := FeedPage{}
	for _, e := range g.GroupFeed.Edges {
		if e.Node == nil || e.Node.Typename != "Story" {
			continue
		}
		p := Post{
			ID:      e.Node.PostID,
			GroupID: gid,
		}
		if p.ID == "" {
			p.ID = e.Node.ID
		}
		if e.Node.Feedback != nil {
			p.FeedbackID = e.Node.Feedback.ID
			if e.Node.Feedback.OwningProfile != nil {
				p.AuthorID = e.Node.Feedback.OwningProfile.ID
				p.AuthorName = e.Node.Feedback.OwningProfile.Name
			}
			if e.Node.Feedback.ReactionCount != nil {
				p.ReactionCount = e.Node.Feedback.ReactionCount.Count
			}
			if e.Node.Feedback.CommentCount != nil {
				p.CommentCount = e.Node.Feedback.CommentCount.TotalCount
			}
			if e.Node.Feedback.ShareCount != nil {
				p.ShareCount = e.Node.Feedback.ShareCount.Count
			}
		}
		if e.Node.Message != nil {
			p.Message = e.Node.Message.Text
		}
		if len(e.Node.Actors) > 0 {
			p.AuthorID = e.Node.Actors[0].ID
			p.AuthorName = e.Node.Actors[0].Name
		}
		if e.Node.CreationTime > 0 {
			p.CreatedAt = time.Unix(e.Node.CreationTime, 0).UTC()
		}
		if e.Node.EditedTime > 0 {
			p.UpdatedAt = time.Unix(e.Node.EditedTime, 0).UTC()
		}
		for _, a := range e.Node.Attachments {
			if len(a.URLs) > 0 {
				p.Attachments = append(p.Attachments, a.URLs...)
			}
		}
		if cs := e.Node.CometSections; cs != nil {
			if cs.Content != nil && cs.Content.Story != nil {
				nested := cs.Content.Story.toPost(gid)
				if p.Message == "" {
					p.Message = nested.Message
				}
				if p.AuthorID == "" {
					p.AuthorID, p.AuthorName = nested.AuthorID, nested.AuthorName
				}
				p.Attachments = append(p.Attachments, nested.Attachments...)
			}
			if cs.Timestamp != nil && cs.Timestamp.Story != nil && p.CreatedAt.IsZero() && cs.Timestamp.Story.CreationTime > 0 {
				p.CreatedAt = time.Unix(cs.Timestamp.Story.CreationTime, 0).UTC()
			}
		}
		p.Attachments = uniqueStrings(p.Attachments)
		page.Posts = append(page.Posts, p)
	}
	if pi := g.GroupFeed.PageInfo; pi != nil {
		page.HasNext = pi.HasNextPage
		page.NextCursor = pi.EndCursor
	}
	return page
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// --- Discover suggestions ---
// Response: viewer.categories.discover_tab.units.edges[].node
// Node types: GroupsTabGroupSuggestionRowUnit (has group), GroupsSuggestionUnit (section header)

type discoverData struct {
	Viewer *struct {
		Categories *struct {
			DiscoverTab *struct {
				Units *struct {
					Edges []discoverEdge `json:"edges"`
				} `json:"units"`
			} `json:"discover_tab"`
		} `json:"categories"`
	} `json:"viewer"`
}

type discoverEdge struct {
	Node json.RawMessage `json:"node"`
}

type discoverGroupRow struct {
	Typename string `json:"__typename"`
	Group    *struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		URL            string `json:"url"`
		MemberCount    int    `json:"member_count"`
		ProfilePicture *struct {
			URI string `json:"uri"`
		} `json:"profile_picture"`
	} `json:"group"`
}

func (d *discoverData) groups() []GroupSearchResult {
	if d.Viewer == nil || d.Viewer.Categories == nil ||
		d.Viewer.Categories.DiscoverTab == nil ||
		d.Viewer.Categories.DiscoverTab.Units == nil {
		return nil
	}
	var out []GroupSearchResult
	for _, e := range d.Viewer.Categories.DiscoverTab.Units.Edges {
		var row discoverGroupRow
		if err := json.Unmarshal(e.Node, &row); err != nil {
			continue
		}
		if row.Group == nil {
			continue
		}
		r := GroupSearchResult{
			ID:          row.Group.ID,
			Name:        row.Group.Name,
			URL:         row.Group.URL,
			MemberCount: row.Group.MemberCount,
		}
		if row.Group.ProfilePicture != nil {
			r.CoverURL = row.Group.ProfilePicture.URI
		}
		out = append(out, r)
	}
	return out
}

// --- My Groups ---

type myGroupsData struct {
	Viewer *struct {
		AllJoinedGroups *struct {
			TabGroupsList *struct {
				Edges []struct {
					Node *fbMyGroupNode `json:"node"`
				} `json:"edges"`
			} `json:"tab_groups_list"`
		} `json:"all_joined_groups"`
	} `json:"viewer"`
}

type fbMyGroupNode struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	ProfilePicture *struct {
		URI string `json:"uri"`
	} `json:"profile_picture"`
}

func (d *myGroupsData) groups() []Group {
	if d.Viewer == nil || d.Viewer.AllJoinedGroups == nil || d.Viewer.AllJoinedGroups.TabGroupsList == nil {
		return nil
	}
	edges := d.Viewer.AllJoinedGroups.TabGroupsList.Edges
	out := make([]Group, 0, len(edges))
	for _, e := range edges {
		if e.Node == nil {
			continue
		}
		g := Group{
			ID:     e.Node.ID,
			Name:   e.Node.Name,
			URL:    e.Node.URL,
			Joined: true,
		}
		if e.Node.ProfilePicture != nil {
			g.CoverURL = e.Node.ProfilePicture.URI
		}
		out = append(out, g)
	}
	return out
}

// --- Group info ---

type groupData struct {
	Group *fbGroup `json:"group"`
}

// --- Cross-group feed (first page) ---
// Response: viewer.groups_tab.cross_group_feed.edges[]

type feedData struct {
	Viewer *struct {
		GroupsTab *struct {
			CrossGroupFeed *struct {
				Edges    []crossFeedEdge `json:"edges"`
				PageInfo *fbPageInfo     `json:"page_info"`
			} `json:"cross_group_feed"`
		} `json:"groups_tab"`
	} `json:"viewer"`
}

type crossFeedEdge struct {
	Node *crossFeedNode `json:"node"`
}

type crossFeedNode struct {
	Typename    string         `json:"__typename"`
	ID          string         `json:"id"`
	PostID      string         `json:"post_id"`
	Feedback    *fbFeedback    `json:"feedback"`
	Attachments []fbAttachment `json:"attachments"`
}

func (d *feedData) toFeedPage() FeedPage {
	if d.Viewer == nil || d.Viewer.GroupsTab == nil || d.Viewer.GroupsTab.CrossGroupFeed == nil {
		return FeedPage{}
	}
	cgf := d.Viewer.GroupsTab.CrossGroupFeed
	page := FeedPage{}
	for _, e := range cgf.Edges {
		if e.Node == nil {
			continue
		}
		p := Post{
			ID: e.Node.PostID,
		}
		if p.ID == "" {
			p.ID = e.Node.ID
		}
		if e.Node.Feedback != nil {
			p.FeedbackID = e.Node.Feedback.ID
			if e.Node.Feedback.OwningProfile != nil {
				p.AuthorID = e.Node.Feedback.OwningProfile.ID
				p.AuthorName = e.Node.Feedback.OwningProfile.Name
			}
			if e.Node.Feedback.AssociatedGroup != nil {
				p.GroupID = e.Node.Feedback.AssociatedGroup.ID
			}
			if e.Node.Feedback.ReactionCount != nil {
				p.ReactionCount = e.Node.Feedback.ReactionCount.Count
			}
			if e.Node.Feedback.CommentCount != nil {
				p.CommentCount = e.Node.Feedback.CommentCount.TotalCount
			}
			if e.Node.Feedback.ShareCount != nil {
				p.ShareCount = e.Node.Feedback.ShareCount.Count
			}
		}
		page.Posts = append(page.Posts, p)
	}
	if pi := cgf.PageInfo; pi != nil {
		page.HasNext = pi.HasNextPage
		page.NextCursor = pi.EndCursor
	}
	return page
}

// --- Cross-group feed pagination ---

type feedPaginationData feedData

func (d *feedPaginationData) toFeedPage() FeedPage {
	return (*feedData)(d).toFeedPage()
}

// --- Join / Leave mutations ---

type joinData struct {
	GroupJoin *struct {
		Group *struct {
			ViewerMembershipStatus string `json:"viewer_actor_membership_status"`
		} `json:"group"`
	} `json:"group_join"`
}

type leaveData struct {
	GroupLeave *struct {
		Group *struct {
			ID string `json:"id"`
		} `json:"group"`
	} `json:"group_leave"`
}

// --- Create group ---

type createGroupData struct {
	GroupCreate *struct {
		Group *fbGroup `json:"group"`
	} `json:"group_create"`
}

// --- Create post ---

type createPostData struct {
	StoryCreate *struct {
		Story *fbStory `json:"story"`
	} `json:"story_create"`
}

// --- Create comment ---

type createCommentData struct {
	CommentCreate *struct {
		Comment *fbComment `json:"comment"`
	} `json:"comment_create"`
}

// --- React ---

type reactData struct {
	FeedbackReact *struct {
		Feedback *struct {
			ID string `json:"id"`
		} `json:"feedback"`
	} `json:"feedback_react"`
}

// --- Comments list ---

type commentsData struct {
	Feedback *struct {
		Comments *struct {
			Edges    []commentEdge `json:"edges"`
			PageInfo *fbPageInfo   `json:"page_info"`
		} `json:"comments"`
	} `json:"feedback"`
}

type commentEdge struct {
	Node *fbComment `json:"node"`
}

func (d *commentsData) toCommentPage() CommentPage {
	if d.Feedback == nil || d.Feedback.Comments == nil {
		return CommentPage{}
	}
	page := CommentPage{}
	for _, e := range d.Feedback.Comments.Edges {
		if e.Node == nil {
			continue
		}
		page.Comments = append(page.Comments, e.Node.toComment())
	}
	if pi := d.Feedback.Comments.PageInfo; pi != nil {
		page.HasNext = pi.HasNextPage
		page.NextCursor = pi.EndCursor
	}
	return page
}

// --- Search ---

type searchData struct {
	SerpResponse *struct {
		Results *struct {
			Edges []searchEdge `json:"edges"`
		} `json:"results"`
	} `json:"serpResponse"`
}

type searchEdge struct {
	// rendering_strategy is at the EDGE level (sibling of node), not nested
	// inside node. Verified against live Facebook SERP response.
	RenderingStrategy *struct {
		ViewModel *struct {
			Profile *struct {
				Typename       string `json:"__typename"`
				ID             string `json:"id"`
				Name           string `json:"name"`
				URL            string `json:"url"`
				ProfilePicture *struct {
					URI string `json:"uri"`
				} `json:"profile_picture"`
			} `json:"profile"`
		} `json:"view_model"`
	} `json:"rendering_strategy"`
}

func (d *searchData) groups() []GroupSearchResult {
	if d.SerpResponse == nil || d.SerpResponse.Results == nil {
		return nil
	}
	var out []GroupSearchResult
	for _, e := range d.SerpResponse.Results.Edges {
		if e.RenderingStrategy == nil ||
			e.RenderingStrategy.ViewModel == nil ||
			e.RenderingStrategy.ViewModel.Profile == nil {
			continue
		}
		p := e.RenderingStrategy.ViewModel.Profile
		if p.Typename != "" && p.Typename != "Group" {
			continue
		}
		r := GroupSearchResult{
			ID:   p.ID,
			Name: p.Name,
			URL:  p.URL,
		}
		if p.ProfilePicture != nil {
			r.CoverURL = p.ProfilePicture.URI
		}
		out = append(out, r)
	}
	return out
}

// --- Members ---

type membersData struct {
	Group *struct {
		ID                  string `json:"id"`
		GroupMemberProfiles *struct {
			Count int `json:"count"`
		} `json:"group_member_profiles"`
		AllActiveMembers *struct {
			Count int `json:"count"`
		} `json:"all_active_members"`
		NewMembers *struct {
			Edges    []memberEdge `json:"edges"`
			PageInfo *fbPageInfo  `json:"page_info"`
		} `json:"new_members"`
	} `json:"group"`
}

type memberEdge struct {
	Node *fbActor `json:"node"`
}

func (d *membersData) toMemberPage() MemberPage {
	if d.Group == nil {
		return MemberPage{}
	}
	page := MemberPage{}
	if d.Group.NewMembers != nil {
		for _, e := range d.Group.NewMembers.Edges {
			if e.Node == nil {
				continue
			}
			page.Members = append(page.Members, Member{
				ID:   e.Node.ID,
				Name: e.Node.Name,
			})
		}
		if pi := d.Group.NewMembers.PageInfo; pi != nil {
			page.HasNext = pi.HasNextPage
			page.NextCursor = pi.EndCursor
		}
	}
	return page
}

func (d *membersData) totalCount() int {
	if d.Group == nil {
		return 0
	}
	if d.Group.AllActiveMembers != nil && d.Group.AllActiveMembers.Count > 0 {
		return d.Group.AllActiveMembers.Count
	}
	if d.Group.GroupMemberProfiles != nil {
		return d.Group.GroupMemberProfiles.Count
	}
	return 0
}

// unmarshalData is a helper that decodes a json.RawMessage into v.
func unmarshalData(raw json.RawMessage, v interface{}) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%w: decoding response data: %v (snippet: %s)",
			ErrRequestFailed, err, truncate(string(raw), 300))
	}
	return nil
}
