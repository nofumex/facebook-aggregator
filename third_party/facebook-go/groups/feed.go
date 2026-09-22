package groups

import (
	"context"
	"fmt"
)

// GetGroupFeed returns the first page of posts from a group's feed in
// reverse-chronological order.
// Returns ErrForbidden for closed or secret groups the user has not joined.
func (c *Client) GetGroupFeed(ctx context.Context, groupID string) (FeedPage, error) {
	if groupID == "" {
		return FeedPage{}, fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}

	vars := mergeVars(map[string]interface{}{
		"feedLocation":                  "GROUP",
		"feedbackSource":                69,
		"focusCommentID":                nil,
		"privacySelectorRenderLocation": "COMET_STREAM",
		"renderLocation":                "groups_tab",
		"scale":                         2,
		"suppress_xac_groups":           false,
	})

	raw, err := c.graphql(ctx, "GroupsCometCrossGroupFeedContainerQuery", vars)
	if err != nil {
		return FeedPage{}, err
	}

	var data feedData
	if err := unmarshalData(raw, &data); err != nil {
		return FeedPage{}, err
	}

	page := data.toFeedPage()
	// Filter to the requested group if specified
	if groupID != "" {
		var filtered []Post
		for _, p := range page.Posts {
			if p.GroupID == groupID || p.GroupID == "" {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) > 0 {
			page.Posts = filtered
		}
	}
	return page, nil
}

// GetGroupFeedPage fetches a subsequent page of group feed posts using the
// cursor returned by a prior GetGroupFeed or GetGroupFeedPage call.
// When FeedPage.HasNext is false, all available posts have been returned.
func (c *Client) GetGroupFeedPage(ctx context.Context, groupID, cursor string) (FeedPage, error) {
	if groupID == "" {
		return FeedPage{}, fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}
	if cursor == "" {
		return FeedPage{}, fmt.Errorf("%w: cursor must not be empty; use GetGroupFeed for the first page", ErrInvalidParams)
	}

	vars := map[string]interface{}{
		"after":          cursor,
		"feedLocation":   "GROUP",
		"feedbackSource": 69,
		"renderLocation": "groups_tab",
		"scale":          1,
	}

	raw, err := c.graphql(ctx, "GroupsCometCrossGroupFeedPaginationQuery", vars)
	if err != nil {
		return FeedPage{}, err
	}

	var data feedPaginationData
	if err := unmarshalData(raw, &data); err != nil {
		return FeedPage{}, err
	}

	return data.toFeedPage(), nil
}

// GetGroupPosts returns posts from a single specific group (as opposed to
// [Client.GetGroupFeed] which returns the cross-group personalised feed).
//
// Use this when you want to read content from one specific group rather
// than the user's home feed across all joined groups.
func (c *Client) GetGroupPosts(ctx context.Context, groupID string) (FeedPage, error) {
	if groupID == "" {
		return FeedPage{}, fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}

	vars := mergeVars(map[string]interface{}{
		"autoOpenChat":                         false,
		"creative_provider_id":                 nil,
		"feedbackSource":                       0,
		"feedLocation":                         "GROUP",
		"feedType":                             "DISCUSSION",
		"filter_topic_id":                      nil,
		"focusCommentID":                       nil,
		"groupID":                              groupID,
		"hasHoistStories":                      false,
		"hoistedSectionHeaderType":             "notifications",
		"hoistStories":                         []interface{}{},
		"hoistStoriesCount":                    0,
		"privacySelectorRenderLocation":        "COMET_STREAM",
		"regular_stories_count":                10,
		"regular_stories_stream_initial_count": 10,
		"renderLocation":                       "group",
		"scale":                                2,
		"shouldDeferMainFeed":                  false,
		// RECENT_ACTIVITY is essential for incremental polling. TOP_POSTS can
		// omit a new low-engagement post from the first page entirely.
		"sortingSetting":  "CHRONOLOGICAL",
		"threadID":        "",
		"useDefaultActor": false,
	})

	raw, err := c.graphql(ctx, "CometGroupDiscussionRootSuccessQuery", vars)
	if err != nil {
		return FeedPage{}, err
	}

	var data singleFeedData
	if err := unmarshalData(raw, &data); err != nil {
		return FeedPage{}, err
	}

	return data.toFeedPage(), nil
}

// GetGroupPostsPage fetches the next page of a single group's discussion feed.
// This small extension is kept in the vendored fork because upstream currently
// exposes only pagination for the personalised cross-group feed.
func (c *Client) GetGroupPostsPage(ctx context.Context, groupID, cursor string) (FeedPage, error) {
	if groupID == "" || cursor == "" {
		return FeedPage{}, fmt.Errorf("%w: groupID and cursor must not be empty", ErrInvalidParams)
	}
	vars := mergeVars(map[string]interface{}{
		"count": 20, "cursor": cursor, "id": groupID, "groupID": groupID,
		"feedLocation": "GROUP", "feedbackSource": 0, "focusCommentID": nil,
		"privacySelectorRenderLocation": "COMET_STREAM",
		"renderLocation":                "group", "sortingSetting": "CHRONOLOGICAL",
		"scale": 2, "useDefaultActor": false,
	})
	raw, err := c.graphql(ctx, "GroupsCometFeedRegularStoriesPaginationQuery", vars)
	if err != nil {
		return FeedPage{}, err
	}
	var data singleFeedData
	if err := unmarshalData(raw, &data); err != nil {
		return FeedPage{}, err
	}
	return data.toFeedPage(), nil
}

// GetPost retrieves a single group post by its story ID.
func (c *Client) GetPost(ctx context.Context, postID string) (*Post, error) {
	if postID == "" {
		return nil, fmt.Errorf("%w: postID must not be empty", ErrInvalidParams)
	}

	vars := mergeVars(map[string]interface{}{
		"storyID":                       postID,
		"hashtag":                       "",
		"useDefaultActor":               false,
		"feedbackSource":                2,
		"focusCommentID":                nil,
		"privacySelectorRenderLocation": "COMET_STREAM",
		"renderLocation":                "permalink",
		"scale":                         2,
	})

	raw, err := c.graphql(ctx, "CometSinglePostDialogContentQuery", vars)
	if err != nil {
		return nil, err
	}

	// The single-post query wraps the story inside data.node or data.story.
	var data struct {
		Node  *fbStory `json:"node"`
		Story *fbStory `json:"story"`
	}
	if err := unmarshalData(raw, &data); err != nil {
		return nil, err
	}
	story := data.Node
	if story == nil {
		story = data.Story
	}
	if story == nil {
		return nil, ErrNotFound
	}

	p := story.toPost("")
	return &p, nil
}
