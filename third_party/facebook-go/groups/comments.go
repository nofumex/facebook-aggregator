package groups

import (
	"context"
	"fmt"
)

// GetPostComments returns the first page of comments on a post.
// feedbackID is Post.FeedbackID (not Post.ID).
func (c *Client) GetPostComments(ctx context.Context, feedbackID string) (CommentPage, error) {
	return c.GetPostCommentsPage(ctx, feedbackID, "")
}

// GetPostCommentsPage fetches a page of comments using cursor-based pagination.
// Pass an empty cursor to get the first page (equivalent to GetPostComments).
func (c *Client) GetPostCommentsPage(ctx context.Context, feedbackID, cursor string) (CommentPage, error) {
	if feedbackID == "" {
		return CommentPage{}, fmt.Errorf("%w: feedbackID must not be empty", ErrInvalidParams)
	}

	type variables struct {
		FeedbackID   string  `json:"feedbackID"`
		Count        int     `json:"count"`
		Cursor       *string `json:"cursor"`
		OrderingMode string  `json:"orderingMode"`
	}

	var cur *string
	if cursor != "" {
		cur = &cursor
	}

	raw, err := c.graphql(ctx, "CommentsListComponentPaginationQuery", variables{
		FeedbackID:   feedbackID,
		Count:        25,
		Cursor:       cur,
		OrderingMode: "social",
	})
	if err != nil {
		return CommentPage{}, err
	}

	var data commentsData
	if err := unmarshalData(raw, &data); err != nil {
		return CommentPage{}, err
	}

	return data.toCommentPage(), nil
}

// CreateComment posts a text comment on the given feedback node.
// feedbackID is Post.FeedbackID (not Post.ID).
func (c *Client) CreateComment(ctx context.Context, feedbackID, message string) (*Comment, error) {
	if feedbackID == "" {
		return nil, fmt.Errorf("%w: feedbackID must not be empty", ErrInvalidParams)
	}
	if message == "" {
		return nil, fmt.Errorf("%w: message must not be empty", ErrInvalidParams)
	}

	type messageInput struct {
		Text string `json:"text"`
	}
	type commentInput struct {
		FeedbackID          string       `json:"feedback_id"`
		Message             messageInput `json:"message"`
		ActorID             string       `json:"actor_id"`
		ClientMutationID    string       `json:"client_mutation_id"`
		FeedbackSource      string       `json:"feedback_source"`
		IsTrackingEncrypted bool         `json:"is_tracking_encrypted"`
		Tracking            string       `json:"tracking"`
	}
	type variables struct {
		Input commentInput `json:"input"`
	}

	raw, err := c.graphql(ctx, "useCometUFICreateCommentMutation", variables{
		Input: commentInput{
			FeedbackID:          feedbackID,
			Message:             messageInput{Text: message},
			ActorID:             c.cookies.CUser,
			ClientMutationID:    mutationID(),
			FeedbackSource:      "NEWSFEED",
			IsTrackingEncrypted: true,
			Tracking:            "{}",
		},
	})
	if err != nil {
		return nil, err
	}

	var data createCommentData
	if err := unmarshalData(raw, &data); err != nil {
		return nil, err
	}
	if data.CommentCreate == nil || data.CommentCreate.Comment == nil {
		return nil, fmt.Errorf("%w: server returned no comment after creation", ErrRequestFailed)
	}

	comment := data.CommentCreate.Comment.toComment()
	return &comment, nil
}

// ReactToPost sets or changes a reaction on a post.
// feedbackID is Post.FeedbackID. Calling on an already-reacted post changes
// the reaction type without error.
func (c *Client) ReactToPost(ctx context.Context, feedbackID string, reaction ReactionType) error {
	if feedbackID == "" {
		return fmt.Errorf("%w: feedbackID must not be empty", ErrInvalidParams)
	}

	type reactInput struct {
		FeedbackID       string `json:"feedback_id"`
		FeedbackReaction string `json:"feedback_reaction"`
		ActorID          string `json:"actor_id"`
		ClientMutationID string `json:"client_mutation_id"`
	}
	type variables struct {
		Input           reactInput `json:"input"`
		UseDefaultActor bool       `json:"useDefaultActor"`
		Scale           int        `json:"scale"`
	}

	raw, err := c.graphql(ctx, "CometUFIFeedbackReactMutation", variables{
		Input: reactInput{
			FeedbackID:       feedbackID,
			FeedbackReaction: string(reaction),
			ActorID:          c.cookies.CUser,
			ClientMutationID: mutationID(),
		},
		UseDefaultActor: false,
		Scale:           1,
	})
	if err != nil {
		return err
	}

	var data reactData
	return unmarshalData(raw, &data)
}
