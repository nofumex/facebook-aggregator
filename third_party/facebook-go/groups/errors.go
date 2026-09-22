package groups

import "errors"

var (
	// ErrInvalidAuth is returned when required cookies (xs or c_user) are missing.
	ErrInvalidAuth = errors.New("facebook/groups: missing required cookie (xs or c_user)")

	// ErrUnauthorized is returned when the session is rejected by Facebook (expired, invalid).
	ErrUnauthorized = errors.New("facebook/groups: authentication failed — session may be expired")

	// ErrForbidden is returned when the authenticated user does not have access to the resource.
	ErrForbidden = errors.New("facebook/groups: access denied")

	// ErrNotFound is returned when the requested group, post, or comment does not exist.
	ErrNotFound = errors.New("facebook/groups: resource not found")

	// ErrRateLimited is returned when Facebook returns HTTP 429.
	ErrRateLimited = errors.New("facebook/groups: rate limited")

	// ErrAlreadyMember is returned when JoinGroup is called on a group the user has already joined.
	ErrAlreadyMember = errors.New("facebook/groups: already a member of this group")

	// ErrNotMember is returned when LeaveGroup is called on a group the user is not a member of.
	ErrNotMember = errors.New("facebook/groups: not a member of this group")

	// ErrInvalidParams is returned when required parameters are missing or invalid.
	ErrInvalidParams = errors.New("facebook/groups: invalid or missing required parameters")

	// ErrPartialResult is returned by ScrapeGroupTrends when the context is cancelled mid-scrape.
	// The caller receives a partial TrendReport alongside this sentinel via errors.Is.
	ErrPartialResult = errors.New("facebook/groups: partial result — context cancelled mid-scrape")

	// ErrRequestFailed is returned when an HTTP request cannot be completed.
	ErrRequestFailed = errors.New("facebook/groups: HTTP request failed")

	// ErrSessionExpired signals that the session tokens have expired mid-flight and a
	// re-bootstrap was attempted. It is used internally; callers see ErrUnauthorized.
	ErrSessionExpired = errors.New("facebook/groups: session expired")

	// ErrDocIDStale is returned when Facebook signals the doc_id is no longer valid.
	// Use WithDocIDs to supply updated identifiers.
	ErrDocIDStale = errors.New("facebook/groups: doc_id is stale — use WithDocIDs to override")
)
