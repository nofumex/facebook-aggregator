# Telegram Rental Aggregator

Telegram-бот агрегирует аренду из публичных каналов через `https://t.me/s/<username>` без пользовательской Telegram-авторизации. Поддерживаются Da Nang и Nha Trang, быстрые cached-страницы, фильтры, избранное, скрытие объявлений, статистика, фоновые подборки и административная панель.

## Запуск

1. Скопируйте `.env.example` в `.env` и заполните `POSTGRES_PASSWORD`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_ADMIN_IDS`, `BASE_URL` и `FREE_LLM_API_KEY`.
2. Запустите:

```bash
docker compose up -d --build
```

Миграции выполняются ботом автоматически. Health checks: `/live` и `/ready`.

## Добавление канала

Откройте `🛠 Админка → Telegram Channels → Добавить`, выберите город и отправьте канал в одном из форматов:

- `https://t.me/lowrentnt`
- `https://t.me/s/lowrentnt`
- `t.me/lowrentnt`
- `@lowrentnt`
- `lowrentnt`

Ссылка нормализуется до canonical username. В фоне бот:

1. получает минимум пять реальных постов;
2. делает единственный LLM-вызов и сохраняет машиноисполняемый `ChannelParsingProfile`;
3. сообщает администратору о готовом профиле;
4. импортирует последние 500 сообщений;
5. при следующих синхронизациях получает только новые сообщения.

LLM больше нигде не вызывается. Каждый пост разбирается локально только регулярными выражениями и mappings из сохранённого профиля канала. Глобального rule/regex fallback нет. Если профиль не извлёк цену и ещё минимум два значения, оригинал сохраняется со статусом `unparsed` и не попадает в пользовательскую выдачу.

## Архитектура

```text
Telegram web preview → per-channel profile parser → PostgreSQL
                                             ├→ city-aware deterministic ranking
                                             ├→ cached collection snapshots
                                             └→ Telegram UI
```

- `internal/telegramfeed` — URL normalization и HTML parsing публичного preview;
- `internal/llm` — только создание профиля при добавлении канала;
- `internal/parser` — локальное исполнение сохранённого профиля;
- `internal/location` — data-driven normalization географии Nha Trang;
- `internal/ranking` — неизменённый Da Nang scoring и отдельный deterministic Nha Trang scoring;
- `internal/storage` — channels, posts, listings, favorites, hidden listings и statistics;
- `internal/collections` — immutable snapshots, обновляемые только в фоне.

Callback query подтверждается до обработки. Scraping, первичный LLM-анализ, импорт и reranking работают в фоновых goroutine и отдельном ограниченном DB pool.

## Данные

Source key — `(channel_username, message_id)`. Хранятся original URL/text, published time и только первая фотография. Карточка содержит кнопку `📨 Открыть объявление` на исходный Telegram-пост.

Nha Trang использует зоны `north / center / south / west`. Resolver различает Oceanus, непосредственное окружение Oceanus и прочий север. Aliases находятся в `internal/location/nha_trang.json` и расширяются без изменения business/ranking logic.

## Тесты

```bash
go test ./...
```

Покрыты Telegram preview parser, нормализация channel URL, создание LLM-профиля одним запросом, локальный profile parser без fallback, география и ranking Nha Trang, а также regression test Da Nang scoring.
