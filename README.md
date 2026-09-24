# Da Nang Rentals Telegram Aggregator

Production-oriented Telegram aggregator for rental posts from Facebook Groups. It polls Facebook over HTTP using an adapted fork of [`teslashibe/facebook-go`](https://github.com/teslashibe/facebook-go), keeps the original post, normalizes Vietnamese/English listing text, ranks offers locally, and exposes search, filters, favorites and curated collections through inline Telegram UI.

The core pipeline works without an LLM. When extraction is enabled, every new unique cleaned `content_hash` is semantically normalized once per extraction schema version before ranking. The optional final collection curator is a separate configuration and never changes normalized facts or scores. Every LLM, discovery, validation, or cache error preserves the rule-based pipeline and stores a retryable extraction status.

## Architecture

```text
Facebook cookies → replaceable Facebook Adapter → per-group scheduler
                                            ↓
                         PostgreSQL transaction + post_id dedup
                                            ↓
        clean text → rule parser → LLM extraction cascade → validation/merge
                                            ↓
                    normalized PostgreSQL → deterministic ranking
                                            ↓
                Telegram inline UI / cached result-page snapshots
                                            ↓ optional
                  OpenAI or OpenAI-compatible shortlist curator
```

Important boundaries:

- `internal/facebook.Adapter` is the application contract. Facebook GraphQL types and `doc_id` values do not escape it.
- `third_party/facebook-go` is a pinned MIT-licensed fork. It fixes chronological group feeds, streamed Relay payload merging, current nested story parsing, media discovery, and group-feed pagination. Replacing it only requires another `Adapter` implementation.
- Telegram handlers only orchestrate UI. Parsing, ranking, sync, collections and persistence are separate packages.
- PostgreSQL owns deduplication (`posts.facebook_post_id UNIQUE`), durable sync state and all user data.
- Extraction cache identity is `(authoritative content_hash, schema_version)`; only the model input is cleaned. Search, rendering, ranking and collections never call the extractor.

## Quick start with Docker Compose

Requirements: Docker with Compose, a Telegram bot token from BotFather, a numeric Telegram user ID for the administrator, and a Facebook account that can read the configured groups.

```bash
cp .env.example .env
# Fill .env; generate SETTINGS_ENCRYPTION_KEY with:
openssl rand -base64 32
docker compose up --build -d
docker compose logs -f bot
```

Open the bot, send `/start`, then use `🛠 Админка → Facebook Groups → Добавить`. The 15 requested starting URLs are in [`configs/starter-groups.txt`](configs/starter-groups.txt); copy and send the whole file contents in one message. They are deliberately data, not hardcoded application behavior, and remain editable in Telegram.

Migrations run automatically on startup and are idempotent. To run them separately:

```bash
go run ./cmd/migrate
```

Health endpoints:

- `GET /live` — process is alive;
- `GET /ready` — PostgreSQL responds within two seconds.

Shutdown on `SIGTERM`/`Ctrl+C` is graceful: HTTP stops accepting work, Telegram long polling exits, and the DB pool closes.

## Local development

Use Go 1.25.13+ and PostgreSQL 16+.

```bash
go test ./...
go run ./cmd/fbcheck -group 253329090046313 -pages 2
go run ./cmd/bot
```

`fbcheck` prints only group/post identifiers, timestamps and byte/media counts; it does not print cookies or post text. `-shape` is a value-redacted protocol diagnostic for Facebook schema changes.

## Semantic extraction

Extraction and final curation have independent configuration. Configure extraction with:

```dotenv
LLM_EXTRACTION_ENABLED=true
LLM_EXTRACTION_BASE_URL=http://159.194.241.69:5173/v1
LLM_EXTRACTION_API_KEY=...
LLM_EXTRACTION_TIMEOUT=25s
LLM_EXTRACTION_CONCURRENCY=2
LLM_EXTRACTION_RETRY_BASE=500ms
LLM_EXTRACTION_MAX_ATTEMPTS=4
LLM_EXTRACTION_SCHEMA_VERSION=rental-v1
LLM_EXTRACTION_MODELS=ministral-3-8b,gpt-oss-20b,gemma-sea-lion-v4-27b,gemini-3.5-flash-lite
LLM_EXTRACTION_AUTO_MODEL=
LLM_EXTRACTION_MODEL_TIMEOUTS=gemma-sea-lion-v4-27b=60s,gemini-3.5-flash-lite=60s
```

On first use the configured order is intersected with IDs returned by authenticated `GET /models`; unavailable IDs are never called. An `auto:<name>` ID in `LLM_EXTRACTION_MODELS` is treated as one ordinary router model. Alternatively, set `LLM_EXTRACTION_AUTO_MODEL=auto:<name>` to call that advertised route at most once after the explicit cascade ends only in network/timeout/408/429/5xx failures. Network, timeout, 408 and 5xx errors retry the same model with bounded 500ms/1s/2s exponential backoff. A 429 immediately advances to the next model (or defers the listing when none remains), and its `Retry-After`/`retryAtMs` creates a shared in-process per-model cooldown. Malformed JSON, invalid types/enums/ranges and contradictions immediately advance to the next explicit model and do not trigger the optional auto fallback. Compatible `json_object` responses may omit nullable fields: the application fills the canonical schema with null/Unknown before validation and caching. If `is_rental_listing` is omitted, compatible mode infers `true` only from rent plus an independent canonical rental fact; location alone and rent alone are insufficient. Structured logs include post ID, model, attempt, latency, validation/fallback reason and token usage. A valid first result stops the cascade.

The extractor receives cleaned `original_text` plus minimal Facebook metadata and rule-parser hints. It returns facts only: rent/deposit/utilities/surcharge are distinct, district is a canonical enum, unknown values remain null, and it cannot set `deal_score`. Typed facts live in `listings`; structured utilities, amenities and restrictions remain JSONB. `llm_enrichments` stores the normalized response and call metadata by content hash and schema version.

After deploying a new schema version, backfill existing rows without stopping the bot:

```bash
docker run --rm --env-file .env --entrypoint /usr/local/bin/extract-backfill facebook-aggregator -batch 100
# Or, when PostgreSQL is the Compose service:
docker compose run --rm --entrypoint /usr/local/bin/extract-backfill bot -batch 100
```

The image contains both `bot` and `extract-backfill`. Add `-limit 5` to process at most five listings total while keeping `-batch` as the database batch size. The command selects only missing/failed/outdated versions, uses a fixed `BACKFILL_CONCURRENCY` worker pool across cache/LLM/benchmark/update work, saves every batch, and recalculates scores. It is safe to restart. The bot also retries due rows automatically using `EXTRACTION_RETRY_INTERVAL`/`EXTRACTION_RETRY_BATCH`; per-row attempts and `next_extraction_retry_at` prevent hot loops. When the API is unavailable, deterministic data is retained and the row remains eligible for a later retry.

Managed PostgreSQL/session poolers default to `DB_MAX_CONNS=5` and `DB_MIN_CONNS=1`; raise these only within the provider's connection budget. The bot partitions that same total budget into a UI pool and a bounded background pool (`DB_BACKGROUND_MAX_CONNS=2` by default), so sync/extraction/reranking/collection refreshes cannot consume Telegram's reserved connections. Background reranking uses `RERANK_INTERVAL` and `RERANK_BATCH`, updating the oldest `ranked_at` rows incrementally.

The 1/7/30-day collections are immutable in-memory snapshots refreshed sequentially in the background. Telegram callbacks only read the latest completed snapshot; reranking and LLM curation never run on the callback path. Configure refresh cadence and a per-period deadline with `COLLECTION_REFRESH_INTERVAL=10m` and `COLLECTION_REFRESH_TIMEOUT=90s`. A failed refresh leaves the previous snapshot available.

## Facebook cookies

Use a dedicated Facebook account with membership only in the groups the service needs. Do not give the bot a Facebook password.

1. Log in to `facebook.com` in a normal browser and confirm the target groups open for that account.
2. Open browser developer tools → Application/Storage → Cookies → `https://www.facebook.com`.
3. Copy the values of `c_user`, `xs`, `sb`, `datr`, `ps_l`, `ps_n`, and (if present) `fr` into the corresponding `FACEBOOK_*` variables in `.env`.
4. Restrict the file (`chmod 600 .env` on Linux), never commit it, and restart the bot.
5. Run `go run ./cmd/fbcheck -group <id-or-slug>` before enabling many groups.

`xs` is the primary session credential and grants the effective access of that logged-in session. Never paste cookies into ordinary Telegram chats or logs. Logging out, changing a password, Facebook checkpoints, or “log out all sessions” can invalidate them. When auth expires the adapter classifies it separately, each group records the error, backoff is applied, and the other groups continue.

On a recognized expired/invalid session, Telegram sends each configured admin one alert with **Вставить новые Cookie** and suppresses duplicate alerts until recovery. Paste the whitespace/tabular cookie table copied from DevTools; the bot immediately deletes that Telegram message, validates the new session, stores the canonical cookie set encrypted as `facebook.cookies`, and hot-swaps the adapter without a restart. It then bypasses `next_poll_at` and queues every enabled group immediately. This recovery pass keeps the normal cursor/overlap but caps each group at the latest 50 posts; subsequent polls return to the ordinary incremental mode. `SETTINGS_ENCRYPTION_KEY` is required for this flow.

The supplied `.gitignore` and `.dockerignore` exclude `.env`. A Telegram message containing an LLM API key is deleted immediately, then the key is AES-256-GCM encrypted in PostgreSQL using `SETTINGS_ENCRYPTION_KEY` and is never displayed back. If that key is not configured, secret updates through the admin UI are rejected.

## Telegram UI

The main UI uses edited messages and inline keyboards:

- `🏠 Новые` — latest listings;
- `🔎 Поиск и фильтры` — composable price, bedrooms, district, beach and foreigner filters;
- free text such as `2 спальни son tra до 6 млн` or `near beach 1pn` is parsed locally;
- `🔥 Подборки` — today, 7 days, 30 days;
- `❤️ Избранное`, hide and details actions;
- listing cards show the live VND equivalent in RUB using the cached official Bank of Russia daily rate;
- the first Facebook photo is the listing card media; `📷 Все фото` sends albums in batches of ten while skipping expired CDN images;
- `🛠 Админка` — visible only to `TELEGRAM_ADMIN_IDS`.

Search result snapshots are cached for five minutes, so `◀️/▶️` pagination only edits the existing Telegram message and does not rerun the database query. Callback queries are acknowledged before work begins; DB, Facebook and LLM tasks execute outside the polling loop.

The group admin shows enable/error status, last successful update, total/new post counts, polling interval and last error. It supports add, rename, enable/disable, connection test, forced sync, per-group/global interval and confirmed deletion.

The LLM admin supports enable/disable, provider (`openai` or `compatible`), base URL, API key, model, timeout, concurrency and connection test. Compatible endpoints must implement `GET /v1/models` and `POST /v1/chat/completions` (adjust the saved base URL when `/v1` differs).

## Normalization and ranking

The rule parser retains `original_text`, raw money mentions and per-field confidence. It handles common Vietnamese/English forms such as `5tr`, `5tr5`, `5tr500`, `5 triệu 500`, `5.5tr`, `5,5 triệu`, `7 củ`, full VND amounts, ranges, deposits and utilities. Price mentions are classified from their clause so electricity/water/deposit values are not treated as rent. Unknown fields do not prevent archival/search storage.

District, property type, bedrooms/studio, area, furnishing, beach distance, amenities, pets, foreigner acceptance, temporary residence and lease terms are extracted best-effort. Add regression examples to `internal/parser/parser_test.go` whenever a new real-world syntax appears.

Comparable medians use up to 90 days, the same district/type, bedrooms within one, and area within ±30%. The exact score is clamped to 0..100:

```text
deal_score = 50
  + 26*clamp((median_rent-rent)/(0.35*median_rent), -1, 1)
  + 12*clamp((median_price_m2-price_m2)/(0.35*median_price_m2), -1, 1)
  + 3*area_preference + 3*bedroom_preference + 3*district_preference
  + 4*furnishing + 4*beach + 4*amenities + 2*utilities
  + 3*(2*exp(-age_days/30)-1) + 5*(2*score_confidence-1)
```

Preferences are explicit, deliberately small adjustments rather than the main value signal. `RankingConfig` centralizes every coefficient and is optionally loaded from `application_settings['ranking.config']`. `score_confidence = 0.55*weighted_field_coverage + 0.25*mean_extraction_confidence + 0.20*min(log(1+comparables)/log(41),1)`. Field coverage weights are price .25, district .15, type .12, bedrooms .12, area .16, furnishing .05 and detailed location .05.

District, bedroom, area-bucket and furnishing defaults are explicit small preference adjustments in `RankingConfig`; relative rent and price/m² remain the dominant terms. Collection reranking is paginated across the whole 1/7/30-day period, then score-ordered candidates are read in bounded pages rather than cutting off the newest 500 rows.

Collections have a stricter admission gate than search: reliable parsed rent, at least two core property facts, `deal_score >= 62`, and score confidence `>= 0.65`. The quota is never padded with incomplete listings. When LLM mode is enabled, only admitted candidates are sent together with their source text and confidence data; the LLM may return fewer candidates or none.

Every unique post is extracted before ranking when extraction is enabled. Merge never replaces known data with null; high-confidence rule scalars win ordinary conflicts, while validated LLM semantics resolve explicitly marked old/current-price ambiguity and complex structured fields. Collections rerank as many as 500 rows across the complete selected 1/7/30-day period before the strict quality gate, so freshness is only a small component rather than an early cutoff.

## Incremental polling behavior

- feeds are requested in `CHRONOLOGICAL` order;
- each group has independent scheduling and exponential error backoff;
- polling walks pages until the saved `last_post_id`/timestamp boundary, with a six-hour overlap;
- the database unique constraint is the final deduplication guard;
- one group failure never cancels another;
- HTTP requests use an adaptive minimum gap, retry and exponential backoff from the Facebook client;
- `FB_DISABLE_HTTP2=true` disables HTTP/2 only on the Facebook client's transport when a specific edge is unstable; other HTTP clients are unaffected;
- session expiry and stale `doc_id` errors have distinct classifications;
- empty protocol artifacts (no text and no media) are not stored.

RUB display conversion is presentation-only. The CBR client has a five-second timeout, bounded retry, a 12-hour successful cache, stale-on-error behavior and a 15-minute negative cache (including HTTP 403), so an outage neither affects ranking nor creates a request storm.

## Unofficial Facebook API limitations

This is not an official Meta API. It can stop working without notice and may be subject to Facebook terms, privacy rules and local law. Only collect data you are authorized to access; use a conservative polling interval and do not evade checkpoints or access controls.

Likely breakpoints are intentionally isolated in the fork/adapter:

1. persisted GraphQL `doc_id` rotation — override with `FB_DOC_IDS` while updating the fork;
2. bootstrap token names (`fb_dtsg`, `lsd`, revision/spin values);
3. query variable contracts and sorting enum values;
4. newline-delimited Relay patch format and patch paths;
5. nested `comet_sections` story/message/actor/media shapes;
6. cookie requirements, headers, anti-bot/rate-limit behavior;
7. permalink or group slug resolution.

The current fork was live-tested with cookie authentication against a configured Da Nang group: bootstrap, numeric and slug resolution, text/timestamp/media extraction, chronological order and cursor pagination all succeeded. This does not guarantee future behavior or every private group.

When Facebook changes, first run `fbcheck -shape`, update only `third_party/facebook-go/groups` or add a new `internal/facebook.Adapter`, then rerun adapter/parser/dedup tests. The scheduler, database, Telegram UI, ranking and LLM code require no Facebook-specific changes.

## Verification checklist

```bash
go test ./...
docker compose config --quiet
docker compose build
go run ./cmd/fbcheck -group <group> -pages 2
```

For a database smoke test, start PostgreSQL and run `go run ./cmd/migrate` twice; both runs must report `migrations: up to date`. Then start the bot and verify `/ready`, `/start`, filters, card pagination, favorite/hide, group connection test/forced sync, and LLM-disabled collections.
