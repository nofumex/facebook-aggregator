-- +goose Up
-- Older posts already contain attachment URLs in their raw payload. Populate
-- the media table so photo pagination works immediately after deployment,
-- without waiting for Facebook to serve those posts again.
INSERT INTO media(post_id, url, position)
SELECT p.id, a.url, (a.position - 1)::integer
FROM posts p
CROSS JOIN LATERAL jsonb_array_elements_text(
    CASE
        WHEN jsonb_typeof(p.raw_payload->'attachments') = 'array' THEN p.raw_payload->'attachments'
        ELSE '[]'::jsonb
    END
) WITH ORDINALITY AS a(url, position)
WHERE a.url ~ '^https?://'
  AND (
      lower(a.url) LIKE '%scontent%'
      OR lower(split_part(a.url, '?', 1)) ~ '\.(jpe?g|png|webp)$'
  )
ON CONFLICT(post_id, url) DO NOTHING;

-- +goose Down
DELETE FROM media m
USING posts p
WHERE m.post_id = p.id
  AND p.raw_payload->'attachments' ? m.url;
