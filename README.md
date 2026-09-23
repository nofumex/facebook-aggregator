# Da Nang Rentals Telegram Aggregator

Production-oriented Telegram aggregator for rental posts from Facebook Groups. It polls Facebook over HTTP using an adapted fork of [`teslashibe/facebook-go`](https://github.com/teslashibe/facebook-go), keeps the original post, normalizes Vietnamese/English listing text, ranks offers locally, and exposes search, filters, favorites and curated collections through inline Telegram UI.

The core pipeline works without an LLM. An LLM is optional and only reorders/explains a small shortlist for `🔥 Подборки`; every LLM error falls back to the local `deal_score`.

## Architecture

```text
Facebook cookies → replaceable Facebook Adapter → per-group scheduler
                                            ↓
                         PostgreSQL transaction + post_id dedup
                                            ↓
                   rule parser → local ranking → indexed listings
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

## Facebook cookies

Use a dedicated Facebook account with membership only in the groups the service needs. Do not give the bot a Facebook password.

1. Log in to `facebook.com` in a normal browser and confirm the target groups open for that account.
2. Open browser developer tools → Application/Storage → Cookies → `https://www.facebook.com`.
3. Copy the values of `c_user`, `xs`, `sb`, `datr`, `ps_l`, `ps_n`, and (if present) `fr` into the corresponding `FACEBOOK_*` variables in `.env`.
4. Restrict the file (`chmod 600 .env` on Linux), never commit it, and restart the bot.
5. Run `go run ./cmd/fbcheck -group <id-or-slug>` before enabling many groups.

`xs` is the primary session credential and grants the effective access of that logged-in session. Never paste cookies into ordinary Telegram chats or logs. Logging out, changing a password, Facebook checkpoints, or “log out all sessions” can invalidate them. When auth expires the adapter classifies it separately, each group records the error, backoff is applied, and the other groups continue. Replace the values in `.env` and restart.

The supplied `.gitignore` and `.dockerignore` exclude `.env`. A Telegram message containing an LLM API key is deleted immediately, then the key is AES-256-GCM encrypted in PostgreSQL using `SETTINGS_ENCRYPTION_KEY` and is never displayed back. If that key is not configured, secret updates through the admin UI are rejected.

## Telegram UI

The main UI uses edited messages and inline keyboards:

- `🏠 Новые` — latest listings;
- `🔎 Поиск и фильтры` — composable price, bedrooms, district, beach and foreigner filters;
- free text such as `2 спальни son tra до 6 млн` or `near beach 1pn` is parsed locally;
- `🔥 Подборки` — today, 7 days, 30 days;
- `❤️ Избранное`, hide and details actions;
- listing cards show the live VND equivalent in RUB using the cached official Bank of Russia daily rate;
- Facebook photos are stored with the post and opened as an in-chat `◀️/▶️` gallery;
- `🛠 Админка` — visible only to `TELEGRAM_ADMIN_IDS`.

Search result snapshots are cached for five minutes, so `◀️/▶️` pagination only edits the existing Telegram message and does not rerun the database query. Callback queries are acknowledged before work begins; DB, Facebook and LLM tasks execute outside the polling loop.

The group admin shows enable/error status, last successful update, total/new post counts, polling interval and last error. It supports add, rename, enable/disable, connection test, forced sync, per-group/global interval and confirmed deletion.

The LLM admin supports enable/disable, provider (`openai` or `compatible`), base URL, API key, model, timeout, concurrency and connection test. Compatible endpoints must implement `GET /v1/models` and `POST /v1/chat/completions` (adjust the saved base URL when `/v1` differs).

## Normalization and ranking

The rule parser retains `original_text`, raw money mentions and per-field confidence. It handles common Vietnamese/English forms such as `5tr`, `5tr5`, `5tr500`, `5 triệu 500`, `5.5tr`, `5,5 triệu`, `7 củ`, full VND amounts, ranges, deposits and utilities. Price mentions are classified from their clause so electricity/water/deposit values are not treated as rent. Unknown fields do not prevent archival/search storage.

District, property type, bedrooms/studio, area, furnishing, beach distance, amenities, pets, foreigner acceptance, temporary residence and lease terms are extracted best-effort. Add regression examples to `internal/parser/parser_test.go` whenever a new real-world syntax appears.

`deal_score` is modular and favors value for money against the median of comparable district/type/bedroom listings over 180 days. It also considers price per m², freshness, completeness, beach, furniture, amenities, utilities, foreigner friendliness, deposit and sample size. Sparse data shrinks the result toward neutral instead of strongly penalizing missing fields. Weights live in `internal/ranking` and can be moved to application settings without changing the scoring interface.

Collections have a stricter admission gate than search: reliable parsed rent, at least two core property facts, `deal_score >= 62`, and score confidence `>= 0.65`. The quota is never padded with incomplete listings. When LLM mode is enabled, only admitted candidates are sent together with their source text and confidence data; the LLM may return fewer candidates or none.

## Incremental polling behavior

- feeds are requested in `CHRONOLOGICAL` order;
- each group has independent scheduling and exponential error backoff;
- polling walks pages until the saved `last_post_id`/timestamp boundary, with a six-hour overlap;
- the database unique constraint is the final deduplication guard;
- one group failure never cancels another;
- HTTP requests use an adaptive minimum gap, retry and exponential backoff from the Facebook client;
- session expiry and stale `doc_id` errors have distinct classifications;
- empty protocol artifacts (no text and no media) are not stored.

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
