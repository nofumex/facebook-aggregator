package groups

import (
	"context"
	"fmt"
)

// GetMembershipQuestions returns the questions a closed group requires
// prospective members to answer before their join request is accepted.
// Returns an empty slice if the group has no questions (open or closed groups
// without screening). Returns ErrNotFound if the group ID is invalid.
func (c *Client) GetMembershipQuestions(ctx context.Context, groupID string) ([]MembershipQuestion, error) {
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

	return data.toQuestions(), nil
}

// JoinGroupWithAnswers submits a join request together with answers to the
// group's membership questions. Use this for gated/closed groups that require
// prospective members to answer questions.
//
// For groups without questions, prefer [Client.JoinGroup] which is simpler.
//
// The answers slice must include one [MembershipAnswer] for each required
// question returned by [Client.GetMembershipQuestions]. Open-ended questions
// take Text; multiple-choice questions take Choice.
func (c *Client) JoinGroupWithAnswers(ctx context.Context, groupID string, answers []MembershipAnswer) error {
	if groupID == "" {
		return fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}

	// Build the answers payload in Facebook's expected shape.
	answerInputs := make([]map[string]interface{}, 0, len(answers))
	for _, a := range answers {
		if a.QuestionID == "" {
			return fmt.Errorf("%w: each answer must include QuestionID", ErrInvalidParams)
		}
		ans := map[string]interface{}{
			"question_id": a.QuestionID,
		}
		if a.Text != "" {
			ans["answer_text"] = a.Text
		}
		if a.Choice != "" {
			ans["answer_choice"] = a.Choice
		}
		answerInputs = append(answerInputs, ans)
	}

	vars := map[string]interface{}{
		"groupID":            groupID,
		"source":             "GROUP_PAGE",
		"membership_answers": answerInputs,
	}

	raw, err := c.graphql(ctx, "useGroupRequestToJoinMutation", vars)
	if err != nil {
		return err
	}

	var data joinData
	return unmarshalData(raw, &data)
}
