package groups

import (
	"context"
	"fmt"
)

// CreatePost publishes a text post to the specified group.
// message must not be empty. Use WithPostAttachmentURL to attach a link.
// Returns ErrForbidden if the user is not a member of the group.
func (c *Client) CreatePost(ctx context.Context, groupID, message string, opts ...PostOption) (*Post, error) {
	if groupID == "" {
		return nil, fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}
	if message == "" {
		return nil, fmt.Errorf("%w: message must not be empty", ErrInvalidParams)
	}

	po := &postOptions{}
	for _, o := range opts {
		o(po)
	}

	type composerContext struct {
		GroupID string `json:"group_id"`
	}
	type textRange struct{}
	type messageInput struct {
		Text   string      `json:"text"`
		Ranges []textRange `json:"ranges"`
	}
	type postInput struct {
		ComposerType     string          `json:"composer_type"`
		ComposerContext  composerContext `json:"composer_context"`
		Message          messageInput    `json:"message"`
		ActorID          string          `json:"actor_id"`
		ClientMutationID string          `json:"client_mutation_id"`
		AttachmentURL    string          `json:"attachment_url,omitempty"`
	}
	type variables struct {
		Input postInput `json:"input"`
	}

	input := postInput{
		ComposerType:     "GROUP",
		ComposerContext:  composerContext{GroupID: groupID},
		Message:          messageInput{Text: message, Ranges: []textRange{}},
		ActorID:          c.cookies.CUser,
		ClientMutationID: mutationID(),
	}
	if po.attachmentURL != "" {
		input.AttachmentURL = po.attachmentURL
	}

	raw, err := c.graphql(ctx, "ComposerStoryCreateMutation", variables{Input: input})
	if err != nil {
		return nil, err
	}

	var data createPostData
	if err := unmarshalData(raw, &data); err != nil {
		return nil, err
	}
	if data.StoryCreate == nil || data.StoryCreate.Story == nil {
		return nil, fmt.Errorf("%w: server returned no story after post creation", ErrRequestFailed)
	}

	p := data.StoryCreate.Story.toPost(groupID)
	return &p, nil
}
