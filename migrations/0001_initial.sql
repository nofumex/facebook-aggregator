-- +goose Up
CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE fb_groups (
    id bigserial PRIMARY KEY,
    facebook_id text UNIQUE,
    url text NOT NULL UNIQUE,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    polling_interval_seconds integer NOT NULL DEFAULT 300 CHECK (polling_interval_seconds BETWEEN 30 AND 86400),
    last_success_at timestamptz,
    last_attempt_at timestamptz,
    last_post_at timestamptz,
    last_post_id text,
    posts_total bigint NOT NULL DEFAULT 0,
    new_posts_last_run integer NOT NULL DEFAULT 0,
    consecutive_errors integer NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT '',
    next_poll_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX fb_groups_due_idx ON fb_groups(next_poll_at) WHERE enabled;

CREATE TABLE posts (
    id bigserial PRIMARY KEY,
    group_id bigint NOT NULL REFERENCES fb_groups(id) ON DELETE CASCADE,
    facebook_post_id text NOT NULL UNIQUE,
    facebook_url text NOT NULL,
    author_id text,
    author_name text,
    original_text text NOT NULL,
    published_at timestamptz NOT NULL,
    facebook_updated_at timestamptz,
    raw_payload jsonb NOT NULL DEFAULT '{}',
    content_hash bytea NOT NULL,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX posts_group_published_idx ON posts(group_id, published_at DESC, id DESC);
CREATE INDEX posts_published_idx ON posts(published_at DESC, id DESC);

CREATE TABLE listings (
    id bigserial PRIMARY KEY,
    post_id bigint NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE,
    rent_min bigint, rent_max bigint, foreigner_price bigint,
    estimated_monthly_total_min bigint, estimated_monthly_total_max bigint,
    currency char(3) NOT NULL DEFAULT 'VND',
    bedrooms smallint, area_m2 numeric(8,2), property_type text,
    district text, street text, address text,
    near_beach boolean, beach_distance_m integer,
    furnished text, amenities jsonb NOT NULL DEFAULT '{}',
    pets_allowed boolean, foreigners_accepted boolean,
    temporary_residence boolean, lease_months smallint, deposit_amount bigint,
    utilities jsonb NOT NULL DEFAULT '{}', raw_values jsonb NOT NULL DEFAULT '{}',
    confidence jsonb NOT NULL DEFAULT '{}',
    deal_score numeric(5,2) NOT NULL DEFAULT 50,
    score_confidence numeric(4,3) NOT NULL DEFAULT 0,
    parser_version text NOT NULL,
    search_vector tsvector GENERATED ALWAYS AS (
      to_tsvector('simple', coalesce(district,'') || ' ' || coalesce(street,'') || ' ' || coalesce(address,'') || ' ' || coalesce(property_type,''))
    ) STORED,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX listings_feed_idx ON listings(deal_score DESC, id DESC);
CREATE INDEX listings_price_idx ON listings(rent_min, deal_score DESC) WHERE rent_min IS NOT NULL;
CREATE INDEX listings_bed_district_real_idx ON listings(bedrooms, district, property_type, deal_score DESC);
CREATE INDEX listings_area_idx ON listings(area_m2) WHERE area_m2 IS NOT NULL;
CREATE INDEX listings_search_idx ON listings USING gin(search_vector);
CREATE INDEX listings_amenities_idx ON listings USING gin(amenities jsonb_path_ops);

CREATE TABLE media (
    id bigserial PRIMARY KEY, post_id bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    url text NOT NULL, kind text NOT NULL DEFAULT 'image', position integer NOT NULL DEFAULT 0,
    UNIQUE(post_id,url)
);
CREATE TABLE parser_results (
    id bigserial PRIMARY KEY, post_id bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    parser_version text NOT NULL, raw_values jsonb NOT NULL, normalized_values jsonb NOT NULL,
    confidence jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX parser_results_post_idx ON parser_results(post_id, created_at DESC);

CREATE TABLE bot_users (
    telegram_user_id bigint PRIMARY KEY, username text, first_name text,
    created_at timestamptz NOT NULL DEFAULT now(), last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE favorites (
    telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,
    listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(telegram_user_id,listing_id)
);
CREATE INDEX favorites_user_time_idx ON favorites(telegram_user_id,created_at DESC);
CREATE TABLE hidden_listings (
    telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,
    listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(telegram_user_id,listing_id)
);
CREATE TABLE user_filters (
    id bigserial PRIMARY KEY, telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,
    name text NOT NULL, filters jsonb NOT NULL, is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE collections (
    id bigserial PRIMARY KEY, period_days integer NOT NULL, algorithm text NOT NULL,
    generated_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
    llm_used boolean NOT NULL DEFAULT false, UNIQUE(period_days, generated_at)
);
CREATE TABLE collection_items (
    collection_id bigint NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    rank integer NOT NULL, reason text NOT NULL DEFAULT '', score numeric(5,2) NOT NULL,
    PRIMARY KEY(collection_id,listing_id), UNIQUE(collection_id,rank)
);
CREATE TABLE llm_analyses (
    id bigserial PRIMARY KEY, provider text NOT NULL, model text NOT NULL, purpose text NOT NULL,
    input_hash bytea NOT NULL, request_metadata jsonb NOT NULL DEFAULT '{}', response jsonb,
    error text, latency_ms integer, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX llm_analyses_hash_idx ON llm_analyses(input_hash,purpose);
CREATE TABLE application_settings (
    key text PRIMARY KEY, value jsonb, encrypted_value bytea, is_secret boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(), CHECK ((value IS NULL) <> (encrypted_value IS NULL))
);
CREATE TABLE sync_runs (
    id bigserial PRIMARY KEY, group_id bigint REFERENCES fb_groups(id) ON DELETE SET NULL,
    started_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz,
    fetched integer NOT NULL DEFAULT 0, inserted integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'running', error_class text, error_message text
);
CREATE INDEX sync_runs_group_time_idx ON sync_runs(group_id,started_at DESC);
CREATE TABLE error_logs (
    id bigserial PRIMARY KEY, component text NOT NULL, group_id bigint REFERENCES fb_groups(id) ON DELETE SET NULL,
    severity text NOT NULL, code text, message text NOT NULL, details jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX error_logs_time_idx ON error_logs(created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS error_logs, sync_runs, application_settings, llm_analyses,
 collection_items, collections, user_filters, hidden_listings, favorites, bot_users,
 parser_results, media, listings, posts, fb_groups, schema_migrations CASCADE;
