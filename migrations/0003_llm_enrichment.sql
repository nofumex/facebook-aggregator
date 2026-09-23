-- +goose Up
ALTER TABLE listings ADD COLUMN IF NOT EXISTS is_rental boolean;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS ward text;

CREATE TABLE IF NOT EXISTS llm_enrichments (
    content_hash bytea PRIMARY KEY,
    provider text NOT NULL,
    response jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS llm_enrichments;
ALTER TABLE listings DROP COLUMN IF EXISTS ward;
ALTER TABLE listings DROP COLUMN IF EXISTS is_rental;
