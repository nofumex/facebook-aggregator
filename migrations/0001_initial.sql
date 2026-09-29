-- +goose Up
CREATE TABLE IF NOT EXISTS schema_migrations(version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());

CREATE TABLE telegram_channels (
  id bigserial PRIMARY KEY,
  username text NOT NULL UNIQUE CHECK (username ~ '^[a-z][a-z0-9_]{4,31}$'),
  url text NOT NULL UNIQUE,
  name text NOT NULL,
  city text NOT NULL CHECK (city IN ('da_nang','nha_trang')),
  enabled boolean NOT NULL DEFAULT true,
  polling_interval_seconds integer NOT NULL DEFAULT 300 CHECK (polling_interval_seconds BETWEEN 30 AND 86400),
  parsing_profile jsonb,
  profile_status text NOT NULL DEFAULT 'pending' CHECK (profile_status IN ('pending','analyzing','ready','error')),
  profile_error text NOT NULL DEFAULT '',
  last_success_at timestamptz, last_attempt_at timestamptz, last_message_at timestamptz,
  last_message_id bigint NOT NULL DEFAULT 0,
  posts_total bigint NOT NULL DEFAULT 0, new_posts_last_run integer NOT NULL DEFAULT 0,
  consecutive_errors integer NOT NULL DEFAULT 0, last_error text NOT NULL DEFAULT '',
  next_poll_at timestamptz NOT NULL DEFAULT now(), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX telegram_channels_due_idx ON telegram_channels(next_poll_at) WHERE enabled;

CREATE TABLE posts (
  id bigserial PRIMARY KEY,
  channel_id bigint NOT NULL REFERENCES telegram_channels(id) ON DELETE CASCADE,
  channel_username text NOT NULL,
  message_id bigint NOT NULL,
  original_url text NOT NULL,
  original_text text NOT NULL,
  published_at timestamptz NOT NULL,
  photo_url text,
  raw_payload jsonb NOT NULL DEFAULT '{}', content_hash bytea NOT NULL,
  first_seen_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(channel_username,message_id)
);
CREATE INDEX posts_channel_published_idx ON posts(channel_id,published_at DESC,id DESC);
CREATE INDEX posts_published_idx ON posts(published_at DESC,id DESC);

CREATE TABLE listings (
  id bigserial PRIMARY KEY, post_id bigint NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE,
  city text NOT NULL CHECK(city IN ('da_nang','nha_trang')), zone text,
  district text, location_original text, street text, address text, building text,
  property_type text, bedrooms smallint, rooms smallint,
  rent_min bigint, rent_max bigint, deposit_amount bigint, lease_months smallint, area_m2 numeric(8,2),
  availability text, utilities jsonb NOT NULL DEFAULT '{}', is_oceanus boolean, near_oceanus boolean,
  confidence jsonb NOT NULL DEFAULT '{}', raw_values jsonb NOT NULL DEFAULT '{}',
  deal_score numeric(5,2) NOT NULL DEFAULT 0, score_confidence numeric(4,3) NOT NULL DEFAULT 0,
  extraction_version text NOT NULL DEFAULT 'channel-profile-v1',
  extraction_status text NOT NULL DEFAULT 'unparsed' CHECK(extraction_status IN ('success','unparsed')),
  profile_parsed_at timestamptz, extraction_attempts integer NOT NULL DEFAULT 0,
  next_extraction_retry_at timestamptz, last_extraction_error text NOT NULL DEFAULT '', ranked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX listings_feed_idx ON listings(city,deal_score DESC,id DESC) WHERE extraction_status='success';
CREATE INDEX listings_search_idx ON listings(city,zone,district,property_type,bedrooms,rent_min);

CREATE TABLE bot_users(telegram_user_id bigint PRIMARY KEY,username text,first_name text,created_at timestamptz NOT NULL DEFAULT now(),last_seen_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE favorites(telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(telegram_user_id,listing_id));
CREATE TABLE hidden_listings(telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(telegram_user_id,listing_id));
CREATE TABLE user_filters(id bigserial PRIMARY KEY,telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,name text NOT NULL,filters jsonb NOT NULL,is_default boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE application_settings(key text PRIMARY KEY,value jsonb,encrypted_value bytea,is_secret boolean NOT NULL DEFAULT false,updated_at timestamptz NOT NULL DEFAULT now(),CHECK((value IS NULL)<>(encrypted_value IS NULL)));
CREATE TABLE sync_runs(id bigserial PRIMARY KEY,channel_id bigint REFERENCES telegram_channels(id) ON DELETE SET NULL,started_at timestamptz NOT NULL DEFAULT now(),finished_at timestamptz,fetched integer NOT NULL DEFAULT 0,inserted integer NOT NULL DEFAULT 0,status text NOT NULL DEFAULT 'running',error_message text);
CREATE TABLE error_logs(id bigserial PRIMARY KEY,component text NOT NULL,channel_id bigint REFERENCES telegram_channels(id) ON DELETE SET NULL,severity text NOT NULL,code text,message text NOT NULL,details jsonb NOT NULL DEFAULT '{}',created_at timestamptz NOT NULL DEFAULT now());

-- +goose Down
DROP TABLE IF EXISTS error_logs,sync_runs,application_settings,user_filters,hidden_listings,favorites,bot_users,listings,posts,telegram_channels,schema_migrations CASCADE;
