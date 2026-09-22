package domain

import (
	"encoding/json"
	"time"
)

type Group struct {
	ID              int64
	FacebookID      string
	URL             string
	Name            string
	Enabled         bool
	PollingInterval time.Duration
	LastSuccessAt   *time.Time
	LastAttemptAt   *time.Time
	LastPostAt      *time.Time
	LastPostID      string
	PostsTotal      int64
	NewPostsLastRun int
	ConsecutiveErrs int
	LastError       string
	NextPollAt      time.Time
	CreatedAt       time.Time
}

type FacebookPost struct {
	ID          string
	GroupID     string
	URL         string
	AuthorID    string
	AuthorName  string
	Text        string
	PublishedAt time.Time
	UpdatedAt   time.Time
	MediaURLs   []string
	Raw         json.RawMessage
}

type Confidence map[string]float64

type Listing struct {
	ID                       int64
	PostID                   int64
	FacebookPostID           string
	FacebookURL              string
	GroupID                  int64
	GroupName                string
	AuthorName               string
	OriginalText             string
	PublishedAt              time.Time
	CreatedAt                time.Time
	RentMin                  *int64
	RentMax                  *int64
	ForeignerPrice           *int64
	EstimatedMonthlyTotalMin *int64
	EstimatedMonthlyTotalMax *int64
	Currency                 string
	Bedrooms                 *int
	AreaM2                   *float64
	PropertyType             string
	District                 string
	Street                   string
	Address                  string
	NearBeach                *bool
	BeachDistanceM           *int
	Furnished                string
	Amenities                map[string]bool
	PetsAllowed              *bool
	ForeignersAccepted       *bool
	TemporaryResidence       *bool
	LeaseMonths              *int
	DepositAmount            *int64
	Utilities                map[string]any
	RawValues                map[string]any
	Confidence               Confidence
	DealScore                float64
	ScoreConfidence          float64
	MediaURLs                []string
}

type SearchFilter struct {
	Query              string
	RentMin            *int64
	RentMax            *int64
	Bedrooms           *int
	AreaMin            *float64
	AreaMax            *float64
	District           string
	PropertyType       string
	Furnished          string
	NearBeach          *bool
	ForeignersAccepted *bool
	FreshAfter         *time.Time
	Sort               string
	Limit              int
	Offset             int
}

type SearchPage struct {
	Items []Listing
	Total int
}

type CollectionItem struct {
	Listing
	Reason string
	Rank   int
}

type DistrictStat struct {
	District      string
	Listings      int
	MedianRent    int64
	MedianPriceM2 int64
}
type MarketSummary struct {
	Listings30d   int
	MedianRent    int64
	MedianPriceM2 int64
	Districts     []DistrictStat
}

type GroupSyncResult struct {
	Fetched    int
	Inserted   int
	NewestID   string
	NewestAt   time.Time
	ReachedOld bool
}
