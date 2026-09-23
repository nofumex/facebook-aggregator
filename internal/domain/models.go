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

// Enrichment is the nullable, evidence-bound structure returned by the LLM.
// Pointer fields distinguish an explicit false/zero from an unknown value.
type Enrichment struct {
	IsRental           *bool          `json:"is_rental_listing"`
	RentMinVND         *int64         `json:"rent_vnd"`
	RentMaxVND         *int64         `json:"rent_max_vnd"`
	Bedrooms           *int           `json:"bedrooms"`
	Rooms              *int           `json:"rooms"`
	PropertyType       *string        `json:"property_type"`
	AreaM2             *float64       `json:"area_m2"`
	District           *string        `json:"district"`
	LocationOriginal   *string        `json:"location_original"`
	Ward               *string        `json:"ward"`
	Street             *string        `json:"street"`
	Address            *string        `json:"address"`
	Building           *string        `json:"building"`
	Furnished          *string        `json:"furnished"`
	NearBeach          *bool          `json:"near_beach"`
	BeachDistanceM     *int           `json:"beach_distance_m"`
	DepositVND         *int64         `json:"deposit_vnd"`
	Utilities          map[string]any `json:"utilities"`
	Amenities          map[string]any `json:"amenities"`
	Restrictions       map[string]any `json:"restrictions"`
	ForeignersAccepted *bool          `json:"foreigners_allowed"`
	ForeignerSurcharge *int64         `json:"foreigner_surcharge_vnd"`
	PetsAllowed        *bool          `json:"pets_allowed"`
	LeaseMonths        *int           `json:"lease_months_min"`
	Confidence         Confidence     `json:"confidence"`
}

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
	IsRental                 *bool
	Bedrooms                 *int
	Rooms                    *int
	AreaM2                   *float64
	PropertyType             string
	District                 string
	Ward                     string
	LocationOriginal         string
	Street                   string
	Address                  string
	Building                 string
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
	Restrictions             map[string]any
	RawValues                map[string]any
	Confidence               Confidence
	DealScore                float64
	ScoreConfidence          float64
	MediaURLs                []string
	ExtractionVersion        string
	ExtractionStatus         string
	LLMExtractedAt           *time.Time
	LLMModel                 string
	ExtractionAttempts       int
	NextExtractionRetryAt    *time.Time
	LastExtractionError      string
	RankedAt                 *time.Time
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
