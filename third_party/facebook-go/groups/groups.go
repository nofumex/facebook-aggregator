package groups

import (
	"context"
	"errors"
	"fmt"
)

// SearchGroups searches Facebook Groups by keyword and returns matching results.
// Use WithSearchLocation and WithSearchLimit to narrow the query.
func (c *Client) SearchGroups(ctx context.Context, query string, opts ...SearchOption) ([]GroupSearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("%w: query must not be empty", ErrInvalidParams)
	}

	so := &searchOptions{limit: 20}
	for _, o := range opts {
		o(so)
	}

	vars := mergeVars(map[string]interface{}{
		"count":           so.limit,
		"allow_streaming": false,
		"args": map[string]interface{}{
			"callsite": "COMET_GLOBAL_SEARCH",
			"config": map[string]interface{}{
				"exact_match":            false,
				"high_confidence_config": nil,
				"intercept_config":       nil,
				"sts_disambiguation":     nil,
				"watch_config":           nil,
			},
			"experience": map[string]interface{}{
				"encoded_server_defined_params": nil,
				"fbid":                          nil,
				"type":                          "GROUPS_TAB",
			},
			"filters": []interface{}{},
			"text":    query,
		},
		"cursor":                        nil,
		"feedLocation":                  "SEARCH",
		"feedbackSource":                23,
		"fetch_filters":                 true,
		"focusCommentID":                nil,
		"locale":                        nil,
		"privacySelectorRenderLocation": "COMET_STREAM",
		"renderLocation":                "search_results_page",
		"scale":                         2,
		"stream_initial_count":          0,
		"useDefaultActor":               false,
	})

	raw, err := c.graphql(ctx, "SearchCometResultsInitialResultsQuery", vars)
	if err != nil {
		return nil, err
	}

	var data searchData
	if err := unmarshalData(raw, &data); err != nil {
		return nil, ErrNotFound
	}

	results := data.groups()
	if len(results) == 0 {
		return nil, ErrNotFound
	}
	return results, nil
}

// DiscoverGroups returns Facebook's personalised group suggestions for the
// authenticated user.
func (c *Client) DiscoverGroups(ctx context.Context) ([]GroupSearchResult, error) {
	vars := mergeVars(map[string]interface{}{
		"scale": 2,
	})

	raw, err := c.graphql(ctx, "GroupsCometDiscoverContentQuery", vars)
	if err != nil {
		return nil, err
	}

	var data discoverData
	if err := unmarshalData(raw, &data); err != nil {
		return nil, err
	}

	return data.groups(), nil
}

// MyGroups returns all groups the authenticated user is a member of, including
// those with pending approval (PendingJoin: true).
func (c *Client) MyGroups(ctx context.Context) ([]Group, error) {
	vars := map[string]interface{}{
		"ordering": []string{"integrity_signals"},
		"scale":    1,
	}

	raw, err := c.graphql(ctx, "GroupsCometJoinsRootQuery", vars)
	if err != nil {
		return nil, err
	}

	var data myGroupsData
	if err := unmarshalData(raw, &data); err != nil {
		return nil, err
	}

	return data.groups(), nil
}

// GetGroup retrieves metadata for a single group by its numeric ID.
// Returns name, profile picture, member count, viewer join state, and whether
// the group requires answering membership questions.
//
// Returns ErrNotFound for nonexistent group IDs and ErrForbidden for secret
// groups the user is not a member of.
func (c *Client) GetGroup(ctx context.Context, groupID string) (*Group, error) {
	if groupID == "" {
		return nil, fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}

	vars := map[string]interface{}{
		"groupID": groupID,
		"scale":   2,
	}

	raw, err := c.graphql(ctx, "GroupsCometParticipationQuestionsDialogQuery", vars)
	if err != nil {
		return nil, err
	}

	var data participationQuestionsData
	if err := unmarshalData(raw, &data); err != nil {
		return nil, err
	}
	if data.Group == nil {
		return nil, ErrNotFound
	}

	g := Group{
		ID:   data.Group.ID,
		Name: data.Group.Name,
	}
	if data.Group.ProfilePicture != nil {
		g.CoverURL = data.Group.ProfilePicture.URI
	}
	// Extract member count from the snippets text (e.g. "33K members • 3 posts a month")
	if data.Group.GroupSnippets != nil {
		for _, e := range data.Group.GroupSnippets.Edges {
			if e.Node != nil && e.Node.Title != nil {
				g.Description = e.Node.Title.Text
				break
			}
		}
	}
	switch data.Group.ViewerForumJoinState {
	case "CAN_JOIN":
		// not a member
	case "MEMBER", "JOINED":
		g.Joined = true
	case "REQUESTED", "PENDING":
		g.PendingJoin = true
	}
	return &g, nil
}

// JoinGroup sends a join request or immediately joins the group (public groups).
// Returns ErrAlreadyMember if the user is already a member.
func (c *Client) JoinGroup(ctx context.Context, groupID string) error {
	if groupID == "" {
		return fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}

	type variables struct {
		GroupID     string `json:"groupID"`
		Surface     string `json:"surface"`
		IsSuggested bool   `json:"isSuggested"`
	}

	raw, err := c.graphql(ctx, "useGroupRequestToJoinMutation", map[string]interface{}{
		"groupID": groupID,
		"source":  "GROUP_PAGE",
	})
	if err != nil {
		if errors.Is(err, ErrAlreadyMember) {
			return ErrAlreadyMember
		}
		return err
	}

	var data joinData
	return unmarshalData(raw, &data)
}

// LeaveGroup removes the authenticated user from the group.
// Returns ErrNotMember if the user is not currently a member.
func (c *Client) LeaveGroup(ctx context.Context, groupID string) error {
	if groupID == "" {
		return fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}

	type variables struct {
		GroupID                    string `json:"groupID"`
		Source                     string `json:"source"`
		RevokeJoinRequestIfPending bool   `json:"revokeJoinRequestIfPending"`
	}

	raw, err := c.graphql(ctx, "useGroupLeaveMutation", map[string]interface{}{
		"groupID":                    groupID,
		"source":                     "GROUP_HEADER_DROPDOWN",
		"revokeJoinRequestIfPending": true,
	})
	if err != nil {
		if errors.Is(err, ErrNotMember) {
			return ErrNotMember
		}
		return err
	}

	var data leaveData
	return unmarshalData(raw, &data)
}

// CreateGroup creates a new Facebook Group and returns its metadata.
// params.Privacy defaults to PrivacyClosed if not specified.
// Returns ErrInvalidParams if params.Name is empty.
func (c *Client) CreateGroup(ctx context.Context, params CreateGroupParams) (*Group, error) {
	if params.Name == "" {
		return nil, fmt.Errorf("%w: Name must not be empty", ErrInvalidParams)
	}
	if params.Privacy == "" {
		params.Privacy = PrivacyClosed
	}

	type createInput struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Privacy     string `json:"privacy"`
	}
	type variables struct {
		Input createInput `json:"input"`
	}

	raw, err := c.graphql(ctx, "useGroupsCometCreateMutation", variables{
		Input: createInput{
			Name:        params.Name,
			Description: params.Description,
			Privacy:     string(params.Privacy),
		},
	})
	if err != nil {
		return nil, err
	}

	var data createGroupData
	if err := unmarshalData(raw, &data); err != nil {
		return nil, err
	}
	if data.GroupCreate == nil || data.GroupCreate.Group == nil {
		return nil, fmt.Errorf("%w: server returned no group after creation", ErrRequestFailed)
	}

	g := data.GroupCreate.Group.toGroup()
	return &g, nil
}
