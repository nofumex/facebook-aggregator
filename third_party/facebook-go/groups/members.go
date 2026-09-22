package groups

import (
	"context"
	"fmt"
)

// GetGroupMembers returns the first page of group members.
// Returns ErrForbidden when the member list is hidden by group settings.
func (c *Client) GetGroupMembers(ctx context.Context, groupID string) (MemberPage, error) {
	return c.GetGroupMembersPage(ctx, groupID, "")
}

// GetGroupMembersPage fetches a subsequent page of group members using the
// cursor returned by a prior call. Pass an empty cursor for the first page.
func (c *Client) GetGroupMembersPage(ctx context.Context, groupID, cursor string) (MemberPage, error) {
	if groupID == "" {
		return MemberPage{}, fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}

	vars := map[string]interface{}{
		"groupID": groupID,
		"scale":   2,
	}
	if cursor != "" {
		vars["cursor"] = cursor
	}

	raw, err := c.graphql(ctx, "GroupsCometMembersRootQuery", vars)
	if err != nil {
		return MemberPage{}, err
	}

	var data membersData
	if err := unmarshalData(raw, &data); err != nil {
		return MemberPage{}, err
	}

	page := data.toMemberPage()
	if count := data.totalCount(); count > 0 && len(page.Members) == 0 {
		page.Members = make([]Member, 0)
	}
	return page, nil
}
