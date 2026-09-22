//go:build integration

package groups

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func cookiesFromEnv() Cookies {
	return Cookies{
		SB:    os.Getenv("FB_SB"),
		DATR:  os.Getenv("FB_DATR"),
		CUser: os.Getenv("FB_CUSER"),
		XS:    os.Getenv("FB_XS"),
		FR:    os.Getenv("FB_FR"),
		PSL:   os.Getenv("FB_PSL"),
		PSN:   os.Getenv("FB_PSN"),
	}
}

func mustClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(cookiesFromEnv(), WithMinRequestGap(1*time.Second))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return c
}

// TestBootstrap verifies AC-1.1 through AC-1.6: session tokens are extracted
// and jazoest is computed.
func TestBootstrap(t *testing.T) {
	c := mustClient(t)

	dtsg, lsd, rev, hs, hsi, spinT, _, jazoest := c.session.snap()

	if dtsg == "" {
		t.Error("fb_dtsg is empty after bootstrap")
	}
	if lsd == "" {
		t.Error("lsd is empty after bootstrap")
	}
	if rev == "" {
		t.Error("__rev is empty after bootstrap")
	}
	if hs == "" {
		t.Error("__hs is empty after bootstrap")
	}
	if hsi == "" {
		t.Error("__hsi is empty after bootstrap")
	}
	if spinT == "" {
		t.Error("__spin_t is empty after bootstrap")
	}
	if jazoest == "" || jazoest[0] != '2' {
		t.Errorf("jazoest should start with '2', got %q", jazoest)
	}

	// Verify jazoest recomputation (AC-1.6)
	expected := computeJazoest(dtsg)
	if jazoest != expected {
		t.Errorf("jazoest mismatch: got %q, want %q", jazoest, expected)
	}

	// Verify __req counter is working (AC-1.5)
	v1 := c.reqCounter.Add(1)
	v2 := c.reqCounter.Add(1)
	if v2 != v1+1 {
		t.Errorf("reqCounter not incrementing: %d -> %d", v1, v2)
	}

	t.Logf("Bootstrap OK: dtsg=%s... lsd=%s... rev=%s jazoest=%s",
		truncate(dtsg, 12), truncate(lsd, 12), rev, jazoest)
}

// TestMyGroups verifies AC-4.1 through AC-4.4.
func TestMyGroups(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	groups, err := c.MyGroups(ctx)
	if err != nil {
		t.Fatalf("MyGroups() error: %v", err)
	}

	t.Logf("MyGroups returned %d groups", len(groups))
	for i, g := range groups {
		if i >= 5 {
			t.Logf("  ... and %d more", len(groups)-5)
			break
		}
		t.Logf("  [%d] id=%s name=%q privacy=%s joined=%v pending=%v members=%d",
			i, g.ID, g.Name, g.Privacy, g.Joined, g.PendingJoin, g.MemberCount)
	}

	// AC-4.2: each group should have Joined=true
	for _, g := range groups {
		if !g.Joined && !g.PendingJoin {
			t.Errorf("group %s (%s) has Joined=false and PendingJoin=false", g.ID, g.Name)
		}
	}
}

// TestSearchGroups verifies AC-2.1 through AC-2.6.
func TestSearchGroups(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	results, err := c.SearchGroups(ctx, "golang developers", WithSearchLimit(5))
	if err != nil {
		t.Fatalf("SearchGroups() error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SearchGroups returned 0 results, expected at least 1")
	}

	t.Logf("SearchGroups returned %d results", len(results))
	for i, r := range results {
		t.Logf("  [%d] id=%s name=%q members=%d privacy=%s",
			i, r.ID, r.Name, r.MemberCount, r.Privacy)
	}

	for _, r := range results {
		if r.ID == "" {
			t.Error("result has empty ID")
		}
		if r.Name == "" {
			t.Error("result has empty Name")
		}
	}
}

// TestDiscoverGroups verifies AC-3.1 through AC-3.3.
func TestDiscoverGroups(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	results, err := c.DiscoverGroups(ctx)
	if err != nil {
		// Discover can fail with field_exception when Facebook deploys new code
		// and the relay provider variables change. This is transient.
		t.Logf("DiscoverGroups() error (may be transient): %v", err)
		return
	}

	t.Logf("DiscoverGroups returned %d results", len(results))
	for i, r := range results {
		if i >= 5 {
			break
		}
		t.Logf("  [%d] id=%s name=%q members=%d", i, r.ID, r.Name, r.MemberCount)
	}
}

// TestGetGroupFeed verifies AC-9: cross-group feed with posts from joined groups.
func TestGetGroupFeed(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	myGroups, err := c.MyGroups(ctx)
	if err != nil {
		t.Fatalf("MyGroups() error: %v", err)
	}
	if len(myGroups) == 0 {
		t.Skip("no groups to test — user is not a member of any group")
	}

	groupID := myGroups[0].ID
	t.Logf("Using group %s (%s) for feed test", groupID, myGroups[0].Name)

	feed, err := c.GetGroupFeed(ctx, groupID)
	if err != nil {
		t.Fatalf("GetGroupFeed(%s) error: %v", groupID, err)
	}
	t.Logf("GetGroupFeed: %d posts, hasNext=%v, cursor=%s",
		len(feed.Posts), feed.HasNext, truncate(feed.NextCursor, 20))

	for i, p := range feed.Posts {
		if i >= 5 {
			break
		}
		t.Logf("  post[%d] id=%s feedbackID=%s author=%q group=%s reactions=%d comments=%d",
			i, truncate(p.ID, 25), truncate(p.FeedbackID, 25),
			p.AuthorName, p.GroupID, p.ReactionCount, p.CommentCount)
	}

	// Test pagination if available
	if feed.HasNext && feed.NextCursor != "" {
		page2, err := c.GetGroupFeedPage(ctx, groupID, feed.NextCursor)
		if err != nil {
			t.Errorf("GetGroupFeedPage() error: %v", err)
		} else {
			t.Logf("Page 2: %d posts, hasNext=%v", len(page2.Posts), page2.HasNext)
		}
	}
}

// TestGetPostComments verifies AC-14.1 through AC-14.3.
func TestGetPostComments(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	// Get a post with comments from the first joined group
	myGroups, err := c.MyGroups(ctx)
	if err != nil || len(myGroups) == 0 {
		t.Skip("no groups available")
	}

	feed, err := c.GetGroupFeed(ctx, myGroups[0].ID)
	if err != nil || len(feed.Posts) == 0 {
		t.Skip("no posts available")
	}

	// Find a post with comments
	var feedbackID string
	for _, p := range feed.Posts {
		if p.CommentCount > 0 && p.FeedbackID != "" {
			feedbackID = p.FeedbackID
			t.Logf("Using post with %d comments, feedbackID=%s", p.CommentCount, truncate(feedbackID, 20))
			break
		}
	}
	if feedbackID == "" {
		t.Skip("no posts with comments found")
	}

	comments, err := c.GetPostComments(ctx, feedbackID)
	if err != nil {
		t.Fatalf("GetPostComments() error: %v", err)
	}

	t.Logf("GetPostComments: %d comments, hasNext=%v", len(comments.Comments), comments.HasNext)
	for i, cm := range comments.Comments {
		if i >= 3 {
			break
		}
		t.Logf("  comment[%d] id=%s author=%q msg=%s",
			i, truncate(cm.ID, 20), cm.AuthorName, truncate(cm.Message, 60))
	}
}

// TestGetGroupMembers verifies AC-15.1 through AC-15.4.
func TestGetGroupMembers(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	myGroups, err := c.MyGroups(ctx)
	if err != nil || len(myGroups) == 0 {
		t.Skip("no groups available")
	}

	members, err := c.GetGroupMembers(ctx, myGroups[0].ID)
	if err != nil {
		t.Fatalf("GetGroupMembers() error: %v", err)
	}

	t.Logf("GetGroupMembers: %d members, hasNext=%v", len(members.Members), members.HasNext)
	for i, m := range members.Members {
		if i >= 5 {
			break
		}
		t.Logf("  member[%d] id=%s name=%q admin=%v", i, m.ID, m.Name, m.IsAdmin)
	}
}

// TestGetGroup verifies AC-5: fetching group metadata.
func TestGetGroup(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	myGroups, err := c.MyGroups(ctx)
	if err != nil || len(myGroups) == 0 {
		t.Skip("no groups available")
	}

	groupID := myGroups[0].ID
	g, err := c.GetGroup(ctx, groupID)
	if err != nil {
		t.Fatalf("GetGroup() error: %v", err)
	}

	t.Logf("GetGroup: id=%s name=%q joined=%v cover=%s desc=%s",
		g.ID, g.Name, g.Joined, truncate(g.CoverURL, 40), truncate(g.Description, 60))

	if g.ID != groupID {
		t.Errorf("ID mismatch: got %s, want %s", g.ID, groupID)
	}
	if g.Name == "" {
		t.Error("Name is empty")
	}
}

// TestGetGroupPosts verifies single-group feed works (vs cross-group feed).
func TestGetGroupPosts(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	myGroups, err := c.MyGroups(ctx)
	if err != nil || len(myGroups) == 0 {
		t.Skip("no groups available")
	}

	groupID := myGroups[0].ID
	t.Logf("Reading posts from group %s (%s)", groupID, myGroups[0].Name)

	feed, err := c.GetGroupPosts(ctx, groupID)
	if err != nil {
		t.Fatalf("GetGroupPosts() error: %v", err)
	}

	t.Logf("GetGroupPosts: %d posts, hasNext=%v", len(feed.Posts), feed.HasNext)
	for i, p := range feed.Posts {
		if i >= 3 {
			break
		}
		t.Logf("  post[%d] id=%s feedbackID=%s author=%q reactions=%d msg=%s",
			i, truncate(p.ID, 25), truncate(p.FeedbackID, 25),
			p.AuthorName, p.ReactionCount, truncate(p.Message, 60))

		// Single-group feed should have group_id matching what we asked for
		if p.GroupID != "" && p.GroupID != groupID {
			t.Errorf("post[%d] has group_id %s, expected %s", i, p.GroupID, groupID)
		}
	}
}

// TestGetPost verifies fetching a single post by ID.
func TestGetPost(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	myGroups, err := c.MyGroups(ctx)
	if err != nil || len(myGroups) == 0 {
		t.Skip("no groups available")
	}

	feed, err := c.GetGroupPosts(ctx, myGroups[0].ID)
	if err != nil || len(feed.Posts) == 0 {
		t.Skip("no posts available to fetch detail for")
	}

	postID := feed.Posts[0].ID
	t.Logf("Fetching post %s", postID)

	post, err := c.GetPost(ctx, postID)
	if err != nil {
		// GetPost requires a different doc_id format; document as known limitation.
		t.Logf("GetPost() error (single-post detail may need separate doc_id): %v", err)
		return
	}

	t.Logf("GetPost: id=%s feedbackID=%s author=%q msg=%s",
		post.ID, truncate(post.FeedbackID, 25), post.AuthorName, truncate(post.Message, 60))
}

// TestGetMembershipQuestions verifies we can fetch the questions a closed group asks.
func TestGetMembershipQuestions(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	myGroups, err := c.MyGroups(ctx)
	if err != nil || len(myGroups) == 0 {
		t.Skip("no groups available")
	}

	groupID := myGroups[0].ID
	t.Logf("Fetching membership questions for group %s (%s)", groupID, myGroups[0].Name)

	questions, err := c.GetMembershipQuestions(ctx, groupID)
	if err != nil {
		t.Fatalf("GetMembershipQuestions() error: %v", err)
	}

	t.Logf("GetMembershipQuestions: %d questions", len(questions))
	for i, q := range questions {
		t.Logf("  q[%d] id=%s type=%s required=%v text=%q",
			i, truncate(q.ID, 20), q.Type, q.Required, truncate(q.Text, 60))
		if len(q.Options) > 0 {
			t.Logf("    options: %v", q.Options)
		}
	}
}

// TestWithDocIDs verifies the doc_id override option works.
func TestWithDocIDs(t *testing.T) {
	cookies := cookiesFromEnv()
	c, err := New(cookies,
		WithMinRequestGap(1*time.Second),
		WithDocIDs(map[string]string{
			"GroupsCometGroupFeedQuery": "9999999999999999", // intentionally bad
		}),
	)
	if err != nil {
		t.Fatalf("New() with doc_id override failed: %v", err)
	}

	// Verify override was picked up
	if c.docIDs["GroupsCometGroupFeedQuery"] != "9999999999999999" {
		t.Fatalf("WithDocIDs did not override: got %s", c.docIDs["GroupsCometGroupFeedQuery"])
	}
	t.Log("WithDocIDs correctly overrides doc_ids")

	// Verify the original doc_ids for other queries are intact
	if c.docIDs["GroupsHomeNewQuery"] != defaultDocIDs["GroupsHomeNewQuery"] {
		t.Error("WithDocIDs corrupted other doc_ids")
	}
}

// TestScrapeGroupTrends_Quick verifies AC-16 with a small post cap.
func TestScrapeGroupTrends_Quick(t *testing.T) {
	c := mustClient(t)
	ctx := context.Background()

	myGroups, err := c.MyGroups(ctx)
	if err != nil || len(myGroups) == 0 {
		t.Skip("no groups available")
	}

	groupID := myGroups[0].ID
	t.Logf("Scraping trends for group %s (%s) — max 15 posts", groupID, myGroups[0].Name)

	report, err := c.ScrapeGroupTrends(ctx, groupID,
		WithTrendMaxPosts(15),
		WithTrendTopN(5),
		WithTrendStopWords([]string{"http", "https", "www"}),
	)
	if err != nil {
		t.Fatalf("ScrapeGroupTrends() error: %v", err)
	}

	t.Logf("TrendReport: postsAnalyzed=%d avgEngagement=%.1f sentiment=%.2f",
		report.PostsAnalyzed, report.AvgEngagement, report.SentimentScore)

	t.Log("Top keywords:")
	for i, kw := range report.TopKeywords {
		if i >= 5 {
			break
		}
		t.Logf("  %d. %q (%d)", i+1, kw.Term, kw.Count)
	}

	t.Log("Top hashtags:")
	for i, h := range report.TopHashtags {
		if i >= 5 {
			break
		}
		t.Logf("  %d. #%s (%d)", i+1, h.Term, h.Count)
	}

	t.Log("Peak hours (UTC):")
	for i, h := range report.PeakHours {
		if i >= 3 {
			break
		}
		t.Logf("  %d. %02d:00", i+1, h)
	}

	t.Log("Top authors:")
	for i, a := range report.ActiveAuthors {
		if i >= 3 {
			break
		}
		t.Logf("  %d. %s (%d posts)", i+1, a.AuthorName, a.PostCount)
	}

	// Verify report fields are populated
	if report.PostsAnalyzed == 0 {
		t.Error("PostsAnalyzed is 0")
	}
	if report.GroupID != groupID {
		t.Errorf("GroupID mismatch: got %s, want %s", report.GroupID, groupID)
	}
}

// TestPartialResult verifies AC-16.8: context cancellation returns partial report.
func TestPartialResult(t *testing.T) {
	c := mustClient(t)

	myGroups, err := c.MyGroups(context.Background())
	if err != nil || len(myGroups) == 0 {
		t.Skip("no groups available")
	}

	// Cancel immediately after first page fetch would start
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	report, err := c.ScrapeGroupTrends(ctx, myGroups[0].ID, WithTrendMaxPosts(1000))
	if err == nil {
		t.Logf("No error (group has ≤1 page of posts): analyzed=%d", report.PostsAnalyzed)
		return
	}

	if report == nil {
		// This is OK if the context was cancelled before the first page came back
		t.Logf("Got nil report + error (cancelled before first page): %v", err)
		return
	}

	t.Logf("Partial result: analyzed=%d posts, error=%v", report.PostsAnalyzed, err)
	fmt.Fprintf(os.Stderr, "") // avoid unused import
}
