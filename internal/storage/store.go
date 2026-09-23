package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/ranking"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ DB *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() { s.DB.Close() }

func scanGroup(row pgx.Row) (domain.Group, error) {
	var g domain.Group
	var seconds int
	err := row.Scan(&g.ID, &g.FacebookID, &g.URL, &g.Name, &g.Enabled, &seconds, &g.LastSuccessAt, &g.LastAttemptAt, &g.LastPostAt, &g.LastPostID, &g.PostsTotal, &g.NewPostsLastRun, &g.ConsecutiveErrs, &g.LastError, &g.NextPollAt, &g.CreatedAt)
	g.PollingInterval = time.Duration(seconds) * time.Second
	return g, err
}

const groupCols = `id,coalesce(facebook_id,''),url,name,enabled,polling_interval_seconds,last_success_at,last_attempt_at,last_post_at,coalesce(last_post_id,''),posts_total,new_posts_last_run,consecutive_errors,last_error,next_poll_at,created_at`

func (s *Store) Groups(ctx context.Context) ([]domain.Group, error) {
	rows, err := s.DB.Query(ctx, "SELECT "+groupCols+" FROM fb_groups ORDER BY enabled DESC,name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) DueGroups(ctx context.Context, limit int) ([]domain.Group, error) {
	rows, err := s.DB.Query(ctx, "SELECT "+groupCols+" FROM fb_groups WHERE enabled AND next_poll_at<=now() ORDER BY next_poll_at LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) Group(ctx context.Context, id int64) (domain.Group, error) {
	return scanGroup(s.DB.QueryRow(ctx, "SELECT "+groupCols+" FROM fb_groups WHERE id=$1", id))
}
func (s *Store) AddGroup(ctx context.Context, fbID, url, name string, poll time.Duration) (domain.Group, error) {
	if poll < 30*time.Second {
		poll = 5 * time.Minute
	}
	row := s.DB.QueryRow(ctx, "INSERT INTO fb_groups(facebook_id,url,name,polling_interval_seconds) VALUES(NULLIF($1,''),$2,$3,$4) RETURNING "+groupCols, fbID, url, name, int(poll.Seconds()))
	return scanGroup(row)
}
func (s *Store) UpdateGroup(ctx context.Context, id int64, name string, enabled bool, poll time.Duration) error {
	_, err := s.DB.Exec(ctx, "UPDATE fb_groups SET name=$2,enabled=$3,polling_interval_seconds=$4,updated_at=now(),next_poll_at=LEAST(next_poll_at,now()) WHERE id=$1", id, name, enabled, int(poll.Seconds()))
	return err
}
func (s *Store) ResolveGroup(ctx context.Context, id int64, fbID, name, url string) error {
	_, err := s.DB.Exec(ctx, "UPDATE fb_groups SET facebook_id=$2,name=CASE WHEN name='' THEN $3 ELSE name END,url=$4,updated_at=now() WHERE id=$1", id, fbID, name, url)
	return err
}
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, "DELETE FROM fb_groups WHERE id=$1", id)
	return err
}
func (s *Store) ForceGroup(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, "UPDATE fb_groups SET next_poll_at=now() WHERE id=$1", id)
	return err
}
func (s *Store) SetAllPolling(ctx context.Context, poll time.Duration) error {
	_, err := s.DB.Exec(ctx, "UPDATE fb_groups SET polling_interval_seconds=$1,updated_at=now()", int(poll.Seconds()))
	return err
}
func (s *Store) SyncStarted(ctx context.Context, groupID int64) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, "INSERT INTO sync_runs(group_id) VALUES($1) RETURNING id", groupID).Scan(&id)
	_, _ = s.DB.Exec(ctx, "UPDATE fb_groups SET last_attempt_at=now() WHERE id=$1", groupID)
	return id, err
}
func (s *Store) SyncFinished(ctx context.Context, runID, groupID int64, r domain.GroupSyncResult, syncErr error, poll time.Duration) {
	status, msg, class := "ok", "", ""
	if syncErr != nil {
		status = "error"
		msg = syncErr.Error()
		class = fmt.Sprintf("%T", syncErr)
	}
	_, _ = s.DB.Exec(ctx, "UPDATE sync_runs SET finished_at=now(),fetched=$2,inserted=$3,status=$4,error_class=NULLIF($5,''),error_message=NULLIF($6,'') WHERE id=$1", runID, r.Fetched, r.Inserted, status, class, msg)
	if syncErr == nil {
		_, _ = s.DB.Exec(ctx, `UPDATE fb_groups SET last_success_at=now(),last_post_at=CASE WHEN $2::timestamptz>'epoch' THEN $2 ELSE last_post_at END,last_post_id=CASE WHEN $3<>'' THEN $3 ELSE last_post_id END,posts_total=posts_total+$4,new_posts_last_run=$4,consecutive_errors=0,last_error='',next_poll_at=now()+make_interval(secs=>$5),updated_at=now() WHERE id=$1`, groupID, r.NewestAt, r.NewestID, r.Inserted, int(poll.Seconds()))
	} else {
		_, _ = s.DB.Exec(ctx, `UPDATE fb_groups SET new_posts_last_run=0,consecutive_errors=consecutive_errors+1,last_error=$2,next_poll_at=now()+make_interval(secs=>LEAST($3*power(2,LEAST(consecutive_errors,6))::int,21600)),updated_at=now() WHERE id=$1`, groupID, msg, int(poll.Seconds()))
	}
}

func (s *Store) InsertListing(ctx context.Context, p domain.FacebookPost, l domain.Listing) (bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	hash := sha256.Sum256([]byte(p.Text))
	var postID int64
	var updated any
	if !p.UpdatedAt.IsZero() {
		updated = p.UpdatedAt
	}
	err = tx.QueryRow(ctx, `INSERT INTO posts(group_id,facebook_post_id,facebook_url,author_id,author_name,original_text,published_at,facebook_updated_at,raw_payload,content_hash) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9,$10) ON CONFLICT(facebook_post_id) DO NOTHING RETURNING id`, l.GroupID, p.ID, p.URL, p.AuthorID, p.AuthorName, p.Text, p.PublishedAt, updated, p.Raw, hash[:]).Scan(&postID)
	isNew := true
	if err == pgx.ErrNoRows {
		isNew = false
		err = tx.QueryRow(ctx, "SELECT id FROM posts WHERE facebook_post_id=$1 FOR UPDATE", p.ID).Scan(&postID)
	}
	if err != nil {
		return false, err
	}
	if !isNew {
		_, err = tx.Exec(ctx, `UPDATE posts SET facebook_url=$2,author_id=NULLIF($3,''),author_name=NULLIF($4,''),original_text=$5,published_at=$6,facebook_updated_at=$7,raw_payload=$8,content_hash=$9,updated_at=now() WHERE id=$1`, postID, p.URL, p.AuthorID, p.AuthorName, p.Text, p.PublishedAt, updated, p.Raw, hash[:])
		if err != nil {
			return false, err
		}
	}
	j := func(v any) []byte { b, _ := json.Marshal(v); return b }
	version := "rules-v2"
	if enriched, _ := l.RawValues["llm_enriched"].(bool); enriched {
		version += "+llm"
	}
	_, err = tx.Exec(ctx, `INSERT INTO listings(post_id,rent_min,rent_max,foreigner_price,estimated_monthly_total_min,estimated_monthly_total_max,currency,is_rental,bedrooms,rooms,area_m2,property_type,district,ward,location_original,street,address,building,near_beach,beach_distance_m,furnished,amenities,pets_allowed,foreigners_accepted,temporary_residence,lease_months,deposit_amount,utilities,restrictions,raw_values,confidence,deal_score,score_confidence,parser_version,extraction_version,extraction_status,llm_extracted_at,llm_model)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),NULLIF($13,''),NULLIF($14,''),NULLIF($15,''),NULLIF($16,''),NULLIF($17,''),NULLIF($18,''),$19,$20,NULLIF($21,''),$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,NULLIF($35,''),COALESCE(NULLIF($36,''),'pending'),$37,NULLIF($38,''))
		ON CONFLICT(post_id) DO UPDATE SET rent_min=$2,rent_max=$3,foreigner_price=$4,estimated_monthly_total_min=$5,estimated_monthly_total_max=$6,currency=$7,is_rental=$8,bedrooms=$9,rooms=$10,area_m2=$11,property_type=NULLIF($12,''),district=NULLIF($13,''),ward=NULLIF($14,''),location_original=NULLIF($15,''),street=NULLIF($16,''),address=NULLIF($17,''),building=NULLIF($18,''),near_beach=$19,beach_distance_m=$20,furnished=NULLIF($21,''),amenities=$22,pets_allowed=$23,foreigners_accepted=$24,temporary_residence=$25,lease_months=$26,deposit_amount=$27,utilities=$28,restrictions=$29,raw_values=$30,confidence=$31,deal_score=$32,score_confidence=$33,parser_version=$34,extraction_version=NULLIF($35,''),extraction_status=COALESCE(NULLIF($36,''),'pending'),llm_extracted_at=$37,llm_model=NULLIF($38,''),updated_at=now()`, postID, l.RentMin, l.RentMax, l.ForeignerPrice, l.EstimatedMonthlyTotalMin, l.EstimatedMonthlyTotalMax, l.Currency, l.IsRental, l.Bedrooms, l.Rooms, l.AreaM2, l.PropertyType, l.District, l.Ward, l.LocationOriginal, l.Street, l.Address, l.Building, l.NearBeach, l.BeachDistanceM, l.Furnished, j(l.Amenities), l.PetsAllowed, l.ForeignersAccepted, l.TemporaryResidence, l.LeaseMonths, l.DepositAmount, j(l.Utilities), j(l.Restrictions), j(l.RawValues), j(l.Confidence), l.DealScore, l.ScoreConfidence, version, l.ExtractionVersion, l.ExtractionStatus, l.LLMExtractedAt, l.LLMModel)
	if err != nil {
		return false, err
	}
	if !isNew {
		if _, err = tx.Exec(ctx, "DELETE FROM media WHERE post_id=$1", postID); err != nil {
			return false, err
		}
	}
	for i, u := range p.MediaURLs {
		_, err = tx.Exec(ctx, "INSERT INTO media(post_id,url,position) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", postID, u, i)
		if err != nil {
			return false, err
		}
	}
	normalized, _ := json.Marshal(l)
	_, err = tx.Exec(ctx, "INSERT INTO parser_results(post_id,parser_version,raw_values,normalized_values,confidence) VALUES($1,$2,$3,$4,$5)", postID, version, j(l.RawValues), normalized, j(l.Confidence))
	if err != nil {
		return false, err
	}
	return isNew, tx.Commit(ctx)
}

func (s *Store) Benchmarks(ctx context.Context, l domain.Listing) (ranking.Benchmarks, error) {
	var b ranking.Benchmarks
	err := s.DB.QueryRow(ctx, `SELECT coalesce(percentile_cont(.5) within group(order by rent_min),0),coalesce(percentile_cont(.5) within group(order by rent_min/nullif(area_m2,0)),0),count(*) FROM listings x JOIN posts p ON p.id=x.post_id WHERE p.published_at>now()-interval '90 days' AND x.is_rental IS DISTINCT FROM false AND x.rent_min IS NOT NULL AND ($1='' OR x.district=$1) AND ($2='' OR x.property_type=$2) AND ($3::smallint IS NULL OR abs(x.bedrooms-$3)<=1) AND ($4::numeric IS NULL OR x.area_m2 IS NULL OR x.area_m2 BETWEEN $4*.7 AND $4*1.3)`, l.District, l.PropertyType, l.Bedrooms, l.AreaM2).Scan(&b.MedianRent, &b.MedianPriceM2, &b.SimilarCount)
	return b, err
}

// RerankPeriod refreshes stored scores against current comparables before a
// collection is selected. The bounded pool keeps this safe for interactive use.
func (s *Store) RerankPeriod(ctx context.Context, after time.Time, engine ranking.Engine, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		limit = 500
	}
	page, err := s.Search(ctx, 0, domain.SearchFilter{FreshAfter: &after, Sort: "new", Limit: limit})
	if err != nil {
		return 0, err
	}
	updated := 0
	for _, l := range page.Items {
		b, e := s.Benchmarks(ctx, l)
		if e != nil {
			return updated, e
		}
		score, confidence := engine.Score(l, b, time.Now())
		if _, e = s.DB.Exec(ctx, "UPDATE listings SET deal_score=$2,score_confidence=$3,updated_at=now() WHERE id=$1", l.ID, score, confidence); e != nil {
			return updated, e
		}
		updated++
	}
	return updated, nil
}

const listingSelect = `SELECT l.id,p.id,p.facebook_post_id,p.facebook_url,p.group_id,g.name,coalesce(p.author_name,''),p.original_text,p.published_at,l.created_at,l.rent_min,l.rent_max,l.foreigner_price,l.estimated_monthly_total_min,l.estimated_monthly_total_max,l.currency,l.is_rental,l.bedrooms,l.rooms,l.area_m2,coalesce(l.property_type,''),coalesce(l.district,''),coalesce(l.ward,''),coalesce(l.location_original,''),coalesce(l.street,''),coalesce(l.address,''),coalesce(l.building,''),l.near_beach,l.beach_distance_m,coalesce(l.furnished,''),l.amenities,l.pets_allowed,l.foreigners_accepted,l.temporary_residence,l.lease_months,l.deposit_amount,l.utilities,l.restrictions,l.raw_values,l.confidence,l.deal_score,l.score_confidence,coalesce((SELECT jsonb_agg(m.url ORDER BY m.position) FROM media m WHERE m.post_id=p.id),'[]'),coalesce(l.extraction_version,''),l.extraction_status,l.llm_extracted_at,coalesce(l.llm_model,'') FROM listings l JOIN posts p ON p.id=l.post_id JOIN fb_groups g ON g.id=p.group_id`

func scanListing(row pgx.Row) (domain.Listing, error) {
	var l domain.Listing
	var amenities, utilities, restrictions, raw, confidence, media []byte
	err := row.Scan(&l.ID, &l.PostID, &l.FacebookPostID, &l.FacebookURL, &l.GroupID, &l.GroupName, &l.AuthorName, &l.OriginalText, &l.PublishedAt, &l.CreatedAt, &l.RentMin, &l.RentMax, &l.ForeignerPrice, &l.EstimatedMonthlyTotalMin, &l.EstimatedMonthlyTotalMax, &l.Currency, &l.IsRental, &l.Bedrooms, &l.Rooms, &l.AreaM2, &l.PropertyType, &l.District, &l.Ward, &l.LocationOriginal, &l.Street, &l.Address, &l.Building, &l.NearBeach, &l.BeachDistanceM, &l.Furnished, &amenities, &l.PetsAllowed, &l.ForeignersAccepted, &l.TemporaryResidence, &l.LeaseMonths, &l.DepositAmount, &utilities, &restrictions, &raw, &confidence, &l.DealScore, &l.ScoreConfidence, &media, &l.ExtractionVersion, &l.ExtractionStatus, &l.LLMExtractedAt, &l.LLMModel)
	if err == nil {
		_ = json.Unmarshal(amenities, &l.Amenities)
		_ = json.Unmarshal(utilities, &l.Utilities)
		_ = json.Unmarshal(restrictions, &l.Restrictions)
		_ = json.Unmarshal(raw, &l.RawValues)
		_ = json.Unmarshal(confidence, &l.Confidence)
		_ = json.Unmarshal(media, &l.MediaURLs)
	}
	return l, err
}
func (s *Store) Listing(ctx context.Context, id int64) (domain.Listing, error) {
	return scanListing(s.DB.QueryRow(ctx, listingSelect+" WHERE l.id=$1", id))
}

func (s *Store) ExtractionBackfillBatch(ctx context.Context, version string, afterID int64, limit int) ([]domain.Listing, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, listingSelect+` WHERE l.id>$1 AND (l.extraction_version IS DISTINCT FROM $2 OR l.extraction_status<>'success') ORDER BY l.id LIMIT $3`, afterID, version, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Listing
	for rows.Next() {
		l, e := scanListing(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) UpdateExtractedListing(ctx context.Context, l domain.Listing) error {
	j := func(v any) []byte { b, _ := json.Marshal(v); return b }
	_, err := s.DB.Exec(ctx, `UPDATE listings SET rent_min=$2,rent_max=$3,foreigner_price=$4,estimated_monthly_total_min=$5,estimated_monthly_total_max=$6,is_rental=$7,bedrooms=$8,rooms=$9,area_m2=$10,property_type=NULLIF($11,''),district=NULLIF($12,''),ward=NULLIF($13,''),location_original=NULLIF($14,''),street=NULLIF($15,''),address=NULLIF($16,''),building=NULLIF($17,''),near_beach=$18,beach_distance_m=$19,furnished=NULLIF($20,''),amenities=$21,pets_allowed=$22,foreigners_accepted=$23,lease_months=$24,deposit_amount=$25,utilities=$26,restrictions=$27,raw_values=$28,confidence=$29,deal_score=$30,score_confidence=$31,extraction_version=NULLIF($32,''),extraction_status=$33,llm_extracted_at=$34,llm_model=NULLIF($35,''),updated_at=now() WHERE id=$1`, l.ID, l.RentMin, l.RentMax, l.ForeignerPrice, l.EstimatedMonthlyTotalMin, l.EstimatedMonthlyTotalMax, l.IsRental, l.Bedrooms, l.Rooms, l.AreaM2, l.PropertyType, l.District, l.Ward, l.LocationOriginal, l.Street, l.Address, l.Building, l.NearBeach, l.BeachDistanceM, l.Furnished, j(l.Amenities), l.PetsAllowed, l.ForeignersAccepted, l.LeaseMonths, l.DepositAmount, j(l.Utilities), j(l.Restrictions), j(l.RawValues), j(l.Confidence), l.DealScore, l.ScoreConfidence, l.ExtractionVersion, l.ExtractionStatus, l.LLMExtractedAt, l.LLMModel)
	return err
}

func (s *Store) Search(ctx context.Context, userID int64, f domain.SearchFilter) (domain.SearchPage, error) {
	args := []any{userID}
	where := []string{"l.is_rental IS DISTINCT FROM false", "NOT EXISTS(SELECT 1 FROM hidden_listings h WHERE h.telegram_user_id=$1 AND h.listing_id=l.id)"}
	add := func(cond string, v any) { args = append(args, v); where = append(where, fmt.Sprintf(cond, len(args))) }
	if f.RentMin != nil {
		add("l.rent_max >= $%d", *f.RentMin)
	}
	if f.RentMax != nil {
		add("l.rent_min <= $%d", *f.RentMax)
	}
	if f.Bedrooms != nil {
		add("l.bedrooms = $%d", *f.Bedrooms)
	}
	if f.AreaMin != nil {
		add("l.area_m2 >= $%d", *f.AreaMin)
	}
	if f.AreaMax != nil {
		add("l.area_m2 <= $%d", *f.AreaMax)
	}
	if f.District != "" {
		add("l.district = $%d", f.District)
	}
	if f.PropertyType != "" {
		add("l.property_type = $%d", f.PropertyType)
	}
	if f.Furnished != "" {
		add("l.furnished = $%d", f.Furnished)
	}
	if f.NearBeach != nil {
		add("l.near_beach = $%d", *f.NearBeach)
	}
	if f.ForeignersAccepted != nil {
		add("l.foreigners_accepted = $%d", *f.ForeignersAccepted)
	}
	if f.FreshAfter != nil {
		add("p.published_at >= $%d", *f.FreshAfter)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		add("(l.search_vector @@ plainto_tsquery('simple',$%d) OR p.original_text ILIKE '%%'||$%d||'%%')", q)
		where[len(where)-1] = fmt.Sprintf("(l.search_vector @@ plainto_tsquery('simple',$%d) OR p.original_text ILIKE '%%'||$%d||'%%')", len(args), len(args))
	}
	w := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRow(ctx, "SELECT count(*) FROM listings l JOIN posts p ON p.id=l.post_id"+w, args...).Scan(&total); err != nil {
		return domain.SearchPage{}, err
	}
	order := "l.deal_score DESC,p.published_at DESC,l.id DESC"
	if f.Sort == "new" {
		order = "p.published_at DESC,l.id DESC"
	} else if f.Sort == "price" {
		order = "l.rent_min ASC NULLS LAST,l.id DESC"
	}
	if f.Limit < 1 || f.Limit > 500 {
		f.Limit = 10
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.DB.Query(ctx, listingSelect+w+fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", order, len(args)-1, len(args)), args...)
	if err != nil {
		return domain.SearchPage{}, err
	}
	defer rows.Close()
	out := domain.SearchPage{Total: total}
	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, l)
	}
	return out, rows.Err()
}

func (s *Store) EnsureUser(ctx context.Context, id int64, username, first string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO bot_users(telegram_user_id,username,first_name) VALUES($1,$2,$3) ON CONFLICT(telegram_user_id) DO UPDATE SET username=$2,first_name=$3,last_seen_at=now()`, id, username, first)
	return err
}
func (s *Store) Favorite(ctx context.Context, user, listing int64) error {
	_, err := s.DB.Exec(ctx, "INSERT INTO favorites(telegram_user_id,listing_id) VALUES($1,$2) ON CONFLICT DO NOTHING", user, listing)
	return err
}
func (s *Store) Hide(ctx context.Context, user, listing int64) error {
	_, err := s.DB.Exec(ctx, "INSERT INTO hidden_listings(telegram_user_id,listing_id) VALUES($1,$2) ON CONFLICT DO NOTHING", user, listing)
	return err
}
func (s *Store) Favorites(ctx context.Context, user int64, limit int) ([]domain.Listing, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, listingSelect+" JOIN favorites f ON f.listing_id=l.id WHERE f.telegram_user_id=$1 ORDER BY f.created_at DESC LIMIT $2", user, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Listing
	for rows.Next() {
		l, e := scanListing(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) Market(ctx context.Context) (domain.MarketSummary, error) {
	var m domain.MarketSummary
	err := s.DB.QueryRow(ctx, `SELECT count(*),coalesce(percentile_cont(.5) within group(order by l.rent_min),0)::bigint,coalesce(percentile_cont(.5) within group(order by l.rent_min/nullif(l.area_m2,0)),0)::bigint FROM listings l JOIN posts p ON p.id=l.post_id WHERE p.published_at>=now()-interval '30 days' AND l.is_rental IS DISTINCT FROM false AND l.rent_min IS NOT NULL`).Scan(&m.Listings30d, &m.MedianRent, &m.MedianPriceM2)
	if err != nil {
		return m, err
	}
	rows, err := s.DB.Query(ctx, `SELECT district,count(*),percentile_cont(.5) within group(order by rent_min)::bigint,coalesce(percentile_cont(.5) within group(order by rent_min/nullif(area_m2,0)),0)::bigint FROM listings l JOIN posts p ON p.id=l.post_id WHERE p.published_at>=now()-interval '30 days' AND l.is_rental IS DISTINCT FROM false AND rent_min IS NOT NULL AND district IS NOT NULL GROUP BY district ORDER BY count(*) DESC LIMIT 8`)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var d domain.DistrictStat
		if err = rows.Scan(&d.District, &d.Listings, &d.MedianRent, &d.MedianPriceM2); err != nil {
			return m, err
		}
		m.Districts = append(m.Districts, d)
	}
	return m, rows.Err()
}
