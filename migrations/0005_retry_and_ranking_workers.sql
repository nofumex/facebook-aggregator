-- +goose Up
ALTER TABLE listings ADD COLUMN IF NOT EXISTS extraction_attempts integer NOT NULL DEFAULT 0;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS next_extraction_retry_at timestamptz DEFAULT now();
ALTER TABLE listings ADD COLUMN IF NOT EXISTS last_extraction_error text;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS ranked_at timestamptz;

UPDATE listings SET next_extraction_retry_at=NULL WHERE extraction_status='success';

CREATE INDEX IF NOT EXISTS listings_extraction_retry_idx
    ON listings(next_extraction_retry_at, id)
    WHERE extraction_status IN ('pending','failed');
CREATE INDEX IF NOT EXISTS listings_extraction_version_idx ON listings(extraction_version, id);
CREATE INDEX IF NOT EXISTS listings_ranked_at_idx
    ON listings(ranked_at, id)
    WHERE is_rental IS DISTINCT FROM false AND rent_min IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS listings_ranked_at_idx, listings_extraction_version_idx, listings_extraction_retry_idx;
ALTER TABLE listings DROP COLUMN IF EXISTS ranked_at, DROP COLUMN IF EXISTS last_extraction_error, DROP COLUMN IF EXISTS next_extraction_retry_at, DROP COLUMN IF EXISTS extraction_attempts;
