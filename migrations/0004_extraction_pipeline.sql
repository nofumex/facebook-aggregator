-- +goose Up
ALTER TABLE listings ADD COLUMN IF NOT EXISTS rooms smallint;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS location_original text;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS building text;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS restrictions jsonb NOT NULL DEFAULT '{}';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS extraction_version text;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS extraction_status text NOT NULL DEFAULT 'pending';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS llm_extracted_at timestamptz;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS llm_model text;
-- Canonical extraction names remain queryable without breaking legacy code
-- and existing data that use rent_min/foreigners_accepted/etc.
ALTER TABLE listings ADD COLUMN IF NOT EXISTS rent_vnd bigint GENERATED ALWAYS AS (rent_min) STORED;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS rent_max_vnd bigint GENERATED ALWAYS AS (rent_max) STORED;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS deposit_vnd bigint GENERATED ALWAYS AS (deposit_amount) STORED;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS foreigners_allowed boolean GENERATED ALWAYS AS (foreigners_accepted) STORED;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS foreigner_surcharge_vnd bigint GENERATED ALWAYS AS (foreigner_price) STORED;

ALTER TABLE llm_enrichments DROP CONSTRAINT IF EXISTS llm_enrichments_pkey;
ALTER TABLE llm_enrichments ADD COLUMN IF NOT EXISTS schema_version text NOT NULL DEFAULT 'rental-v1';
ALTER TABLE llm_enrichments ADD COLUMN IF NOT EXISTS model text;
ALTER TABLE llm_enrichments ADD COLUMN IF NOT EXISTS latency_ms integer;
ALTER TABLE llm_enrichments ADD COLUMN IF NOT EXISTS input_tokens integer;
ALTER TABLE llm_enrichments ADD COLUMN IF NOT EXISTS output_tokens integer;
ALTER TABLE llm_enrichments ADD PRIMARY KEY(content_hash, schema_version);

UPDATE listings SET district = CASE district
    WHEN 'Sơn Trà' THEN 'Son Tra'
    WHEN 'Ngũ Hành Sơn' THEN 'Ngu Hanh Son'
    WHEN 'Hải Châu' THEN 'Hai Chau'
    WHEN 'Thanh Khê' THEN 'Thanh Khe'
    WHEN 'Liên Chiểu' THEN 'Lien Chieu'
    WHEN 'Cẩm Lệ' THEN 'Cam Le'
    WHEN 'Hòa Vang' THEN 'Hoa Vang'
    ELSE district END;
UPDATE listings SET furnished='partial' WHERE furnished='basic';

CREATE INDEX IF NOT EXISTS listings_property_type_idx ON listings(property_type) WHERE property_type IS NOT NULL;
CREATE INDEX IF NOT EXISTS listings_district_idx ON listings(district) WHERE district IS NOT NULL;
CREATE INDEX IF NOT EXISTS listings_furnished_idx ON listings(furnished) WHERE furnished IS NOT NULL;
CREATE INDEX IF NOT EXISTS listings_collection_idx ON listings(district,property_type,bedrooms,deal_score DESC) WHERE rent_min IS NOT NULL;
CREATE INDEX IF NOT EXISTS listings_rent_idx ON listings(rent_min) WHERE rent_min IS NOT NULL;
CREATE INDEX IF NOT EXISTS listings_area_idx ON listings(area_m2) WHERE area_m2 IS NOT NULL;
CREATE INDEX IF NOT EXISTS listings_bedrooms_idx ON listings(bedrooms) WHERE bedrooms IS NOT NULL;
CREATE INDEX IF NOT EXISTS listings_score_idx ON listings(deal_score DESC);
CREATE INDEX IF NOT EXISTS posts_published_at_idx ON posts(published_at DESC);
CREATE INDEX IF NOT EXISTS listings_extraction_pending_idx ON listings(extraction_status,post_id) WHERE extraction_status <> 'success';

-- +goose Down
DROP INDEX IF EXISTS posts_published_at_idx, listings_score_idx, listings_bedrooms_idx, listings_area_idx, listings_rent_idx, listings_extraction_pending_idx, listings_collection_idx, listings_furnished_idx, listings_district_idx, listings_property_type_idx;
ALTER TABLE llm_enrichments DROP CONSTRAINT IF EXISTS llm_enrichments_pkey;
ALTER TABLE llm_enrichments DROP COLUMN IF EXISTS output_tokens, DROP COLUMN IF EXISTS input_tokens, DROP COLUMN IF EXISTS latency_ms, DROP COLUMN IF EXISTS model, DROP COLUMN IF EXISTS schema_version;
ALTER TABLE llm_enrichments ADD PRIMARY KEY(content_hash);
ALTER TABLE listings DROP COLUMN IF EXISTS foreigner_surcharge_vnd, DROP COLUMN IF EXISTS foreigners_allowed, DROP COLUMN IF EXISTS deposit_vnd, DROP COLUMN IF EXISTS rent_max_vnd, DROP COLUMN IF EXISTS rent_vnd, DROP COLUMN IF EXISTS llm_model, DROP COLUMN IF EXISTS llm_extracted_at, DROP COLUMN IF EXISTS extraction_status, DROP COLUMN IF EXISTS extraction_version, DROP COLUMN IF EXISTS restrictions, DROP COLUMN IF EXISTS building, DROP COLUMN IF EXISTS location_original, DROP COLUMN IF EXISTS rooms;
