package facebook

import (
	"context"
	"errors"
	"time"

	"github.com/egori/facebook-aggregator/internal/domain"
)

var (
	ErrAuthentication  = errors.New("facebook authentication expired or invalid")
	ErrRateLimited     = errors.New("facebook rate limited")
	ErrForbidden       = errors.New("facebook group is unavailable to this account")
	ErrProtocolChanged = errors.New("facebook internal API changed")
)

type FetchRequest struct {
	GroupID    string
	StopPostID string
	StopBefore time.Time
	MaxPages   int
	Overlap    time.Duration
}

type FetchResult struct {
	Posts      []domain.FacebookPost
	NewestID   string
	NewestAt   time.Time
	ReachedOld bool
}

// Adapter is the only contract the application has with Facebook. Internal
// GraphQL details, doc_ids and cookies never leak into business logic.
type Adapter interface {
	ResolveGroup(ctx context.Context, idOrSlug string) (id, name, canonicalURL string, err error)
	FetchRecent(ctx context.Context, req FetchRequest) (FetchResult, error)
	Check(ctx context.Context, groupID string) error
	Name() string
}

type UnavailableAdapter struct{ Reason error }

func (a UnavailableAdapter) ResolveGroup(context.Context, string) (string, string, string, error) {
	return "", "", "", a.Reason
}
func (a UnavailableAdapter) FetchRecent(context.Context, FetchRequest) (FetchResult, error) {
	return FetchResult{}, a.Reason
}
func (a UnavailableAdapter) Check(context.Context, string) error { return a.Reason }
func (a UnavailableAdapter) Name() string                        { return "unavailable" }
