package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egori/facebook-aggregator/internal/collections"
	"github.com/egori/facebook-aggregator/internal/domain"
	fbadapter "github.com/egori/facebook-aggregator/internal/facebook"
	"github.com/egori/facebook-aggregator/internal/llm"
	"github.com/egori/facebook-aggregator/internal/parser"
	"github.com/egori/facebook-aggregator/internal/secrets"
	"github.com/egori/facebook-aggregator/internal/storage"
	"github.com/egori/facebook-aggregator/internal/syncer"
)

type Bot struct {
	api         *Client
	store       *storage.Store
	sync        *syncer.Service
	fb          fbadapter.Adapter
	collections *collections.Service
	admins      map[int64]bool
	cipher      *secrets.Cipher
	log         *slog.Logger
	defaultPoll time.Duration
	mu          sync.Mutex
	states      map[int64]string
	filters     map[int64]domain.SearchFilter
	pages       map[string]pageCache
}
type pageCache struct {
	items   []domain.Listing
	total   int
	user    int64
	until   time.Time
	title   string
	reasons map[int64]string
	filter  *domain.SearchFilter
}

func NewBot(api *Client, store *storage.Store, sync *syncer.Service, fb fbadapter.Adapter, c *collections.Service, admins map[int64]bool, cipher *secrets.Cipher, log *slog.Logger, poll time.Duration) *Bot {
	return &Bot{api: api, store: store, sync: sync, fb: fb, collections: c, admins: admins, cipher: cipher, log: log, defaultPoll: poll, states: map[int64]string{}, filters: map[int64]domain.SearchFilter{}, pages: map[string]pageCache{}}
}

func (b *Bot) Run(ctx context.Context) error {
	offset := 0
	for {
		updates, err := b.api.Updates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			b.log.Warn("telegram polling", "error", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.Callback != nil {
				_ = b.api.Answer(context.WithoutCancel(ctx), u.Callback.ID, "")
				go b.callback(context.WithoutCancel(ctx), u.Callback)
			} else if u.Message != nil {
				go b.message(context.WithoutCancel(ctx), u.Message)
			}
		}
	}
}
func (b *Bot) message(ctx context.Context, m *Message) {
	_ = b.store.EnsureUser(ctx, m.From.ID, m.From.Username, m.From.FirstName)
	text := strings.TrimSpace(m.Text)
	if text == "/start" || text == "/menu" {
		b.showMenu(ctx, m.Chat.ID, 0)
		return
	}
	b.mu.Lock()
	state := b.states[m.From.ID]
	delete(b.states, m.From.ID)
	b.mu.Unlock()
	if state != "" {
		b.handleState(ctx, m, state)
		return
	}
	if strings.HasPrefix(text, "/") {
		b.send(ctx, m.Chat.ID, "Неизвестная команда. Используйте меню.", mainKeyboard(b.admins[m.From.ID]))
		return
	}
	f := parser.ParseSearch(text)
	b.mu.Lock()
	b.filters[m.From.ID] = f
	b.mu.Unlock()
	b.runSearch(ctx, m.Chat.ID, 0, m.From.ID, f, "🔎 Результаты поиска")
}
func (b *Bot) callback(ctx context.Context, q *CallbackQuery) {
	_ = b.store.EnsureUser(ctx, q.From.ID, q.From.Username, q.From.FirstName)
	parts := strings.Split(q.Data, ":")
	switch parts[0] {
	case "menu":
		b.showMenu(ctx, q.Message.Chat.ID, q.Message.MessageID)
	case "new":
		f := domain.SearchFilter{Limit: 20, Sort: "new"}
		b.runSearch(ctx, q.Message.Chat.ID, q.Message.MessageID, q.From.ID, f, "🏠 Новые объявления")
	case "search":
		b.showFilters(ctx, q)
	case "filter":
		b.applyFilter(ctx, q, parts)
	case "find":
		b.mu.Lock()
		f := b.filters[q.From.ID]
		b.mu.Unlock()
		b.runSearch(ctx, q.Message.Chat.ID, q.Message.MessageID, q.From.ID, f, "🔎 Подходящие варианты")
	case "page":
		b.showPage(ctx, q.Message.Chat.ID, q.Message.MessageID, q.From.ID, parts)
	case "save":
		b.listAction(ctx, q, true, parts)
	case "hide":
		b.listAction(ctx, q, false, parts)
	case "detail":
		b.showDetails(ctx, q, parts)
	case "collections":
		b.showCollections(ctx, q)
	case "col":
		b.runCollection(ctx, q, parts)
	case "admin":
		if b.admins[q.From.ID] {
			b.showAdmin(ctx, q)
		}
	case "agroups":
		if b.admins[q.From.ID] {
			b.showGroups(ctx, q)
		}
	case "ag":
		if b.admins[q.From.ID] {
			b.showGroup(ctx, q, parts)
		}
	case "aadd":
		if b.admins[q.From.ID] {
			b.setState(q.From.ID, "add_group")
			b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "Пришлите URL или ID группы. Можно несколько — по одному в строке.", back("agroups"))
		}
	case "async":
		if b.admins[q.From.ID] && len(parts) > 1 {
			if id, _ := strconv.ParseInt(parts[1], 10, 64); b.sync.Trigger(id) {
				b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "Синхронизация поставлена в очередь.", back("agroups"))
			}
		}
	case "atoggle":
		if b.admins[q.From.ID] {
			b.toggleGroup(ctx, q, parts)
		}
	case "adel":
		if b.admins[q.From.ID] {
			b.deleteGroup(ctx, q, parts)
		}
	case "arename":
		if b.admins[q.From.ID] && len(parts) > 1 {
			b.setState(q.From.ID, "rename:"+parts[1])
			b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "Пришлите новое название.", back("ag:"+parts[1]))
		}
	case "apoll":
		if b.admins[q.From.ID] && len(parts) > 1 {
			b.setState(q.From.ID, "poll:"+parts[1])
			b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "Пришлите интервал, например <code>5m</code> или <code>1h</code>.", back("ag:"+parts[1]))
		}
	case "apollall":
		if b.admins[q.From.ID] {
			b.setState(q.From.ID, "poll_global")
			b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "Пришлите глобальный интервал, например <code>5m</code>.", back("agroups"))
		}
	case "acheck":
		if b.admins[q.From.ID] {
			b.checkGroup(ctx, q, parts)
		}
	case "llm":
		if b.admins[q.From.ID] {
			b.showLLM(ctx, q)
		}
	case "llmset":
		if b.admins[q.From.ID] {
			b.llmSet(ctx, q, parts)
		}
	case "llmcheck":
		if b.admins[q.From.ID] {
			b.checkLLM(ctx, q)
		}
	}
}

func (b *Bot) showMenu(ctx context.Context, chat int64, msg int) {
	rows := [][]Button{{cb("🏠 Новые", "new"), cb("🔎 Поиск и фильтры", "search")}, {cb("🔥 Подборки", "collections"), cb("❤️ Избранное", "filter:favorites")}, {cb("📊 Рынок / статистика", "filter:market"), cb("⚙️ Настройки", "filter:settings")}}
	if b.admins[chat] {
		rows = append(rows, []Button{cb("🛠 Админка", "admin")})
	}
	b.editOrSend(ctx, chat, msg, "<b>Аренда в Дананге</b>\n\nСвежие объявления из Facebook Groups, нормализованные и отсортированные по выгодности.", Markup{rows})
}
func mainKeyboard(admin bool) Markup {
	rows := [][]Button{{cb("🏠 Новые", "new"), cb("🔎 Поиск", "search")}, {cb("🔥 Подборки", "collections"), cb("❤️ Избранное", "filter:favorites")}}
	if admin {
		rows = append(rows, []Button{cb("🛠 Админка", "admin")})
	}
	return Markup{rows}
}
func (b *Bot) showFilters(ctx context.Context, q *CallbackQuery) {
	b.mu.Lock()
	f := b.filters[q.From.ID]
	b.mu.Unlock()
	text := "<b>🔎 Поиск и фильтры</b>\n\n" + filterSummary(f) + "\n\nМожно также просто написать: <code>2 спальни son tra до 6 млн</code>"
	k := Markup{[][]Button{{cb("до 5 млн", "filter:max:5000000"), cb("до 7 млн", "filter:max:7000000"), cb("до 10 млн", "filter:max:10000000")}, {cb("Studio", "filter:beds:0"), cb("1 спальня", "filter:beds:1"), cb("2 спальни", "filter:beds:2")}, {cb("Sơn Trà", "filter:district:Sơn Trà"), cb("Ngũ Hành Sơn", "filter:district:Ngũ Hành Sơn")}, {cb("Квартира", "filter:type:apartment"), cb("Дом", "filter:type:house"), cb("Комната", "filter:type:room")}, {cb("от 30 м²", "filter:area:30"), cb("от 50 м²", "filter:area:50"), cb("С мебелью", "filter:furnished:full")}, {cb("🌊 У моря", "filter:beach:1"), cb("🌍 Для иностранцев", "filter:foreign:1")}, {cb("Сначала выгодные", "filter:sort:score"), cb("Сначала новые", "filter:sort:new")}, {cb("✅ Показать", "find"), cb("♻️ Сбросить", "filter:reset")}, {cb("← Меню", "menu")}}}
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, text, k)
}
func (b *Bot) applyFilter(ctx context.Context, q *CallbackQuery, p []string) {
	if len(p) < 2 {
		return
	}
	if p[1] == "favorites" {
		b.runFavorites(ctx, q)
		return
	}
	if p[1] == "market" {
		b.showMarket(ctx, q)
		return
	}
	if p[1] == "settings" {
		b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "⚙️ Пользовательские фильтры сохраняются в текущей сессии. Постоянные подписки будут использовать ту же модель фильтров.", back("menu"))
		return
	}
	b.mu.Lock()
	f := b.filters[q.From.ID]
	if p[1] == "reset" {
		f = domain.SearchFilter{Limit: 20, Sort: "score"}
	} else if len(p) > 2 {
		switch p[1] {
		case "max":
			v, _ := strconv.ParseInt(p[2], 10, 64)
			f.RentMax = &v
		case "beds":
			v, _ := strconv.Atoi(p[2])
			f.Bedrooms = &v
		case "district":
			f.District = p[2]
		case "type":
			f.PropertyType = p[2]
		case "area":
			v, _ := strconv.ParseFloat(p[2], 64)
			f.AreaMin = &v
		case "furnished":
			f.Furnished = p[2]
		case "sort":
			f.Sort = p[2]
		case "beach":
			v := true
			f.NearBeach = &v
		case "foreign":
			v := true
			f.ForeignersAccepted = &v
		}
	}
	b.filters[q.From.ID] = f
	b.mu.Unlock()
	b.showFilters(ctx, q)
}

func (b *Bot) runSearch(ctx context.Context, chat int64, msg int, user int64, f domain.SearchFilter, title string) {
	f.Limit = 50
	f.Offset = 0
	page, err := b.store.Search(ctx, user, f)
	if err != nil {
		b.fail(ctx, chat, msg, err)
		return
	}
	b.cacheAndShow(ctx, chat, msg, user, page.Items, page.Total, title, nil, &f)
}
func (b *Bot) cacheAndShow(ctx context.Context, chat int64, msg int, user int64, items []domain.Listing, total int, title string, reasons map[int64]string, filter *domain.SearchFilter) {
	if len(items) == 0 {
		b.editOrSend(ctx, chat, msg, title+"\n\nНичего не найдено. Попробуйте ослабить фильтры.", back("menu"))
		return
	}
	token := shortToken()
	b.mu.Lock()
	b.pages[token] = pageCache{items: items, total: total, user: user, until: time.Now().Add(5 * time.Minute), title: title, reasons: reasons, filter: filter}
	for k, v := range b.pages {
		if time.Now().After(v.until) {
			delete(b.pages, k)
		}
	}
	b.mu.Unlock()
	b.renderCard(ctx, chat, msg, token, 0)
}
func (b *Bot) showPage(ctx context.Context, chat int64, msg int, user int64, p []string) {
	if len(p) < 3 {
		return
	}
	idx, _ := strconv.Atoi(p[2])
	b.mu.Lock()
	c, ok := b.pages[p[1]]
	b.mu.Unlock()
	if !ok || c.user != user || time.Now().After(c.until) {
		b.editOrSend(ctx, chat, msg, "Результаты устарели — откройте поиск снова.", back("menu"))
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(c.items) && idx < c.total && c.filter != nil {
		nextFilter := *c.filter
		nextFilter.Offset = len(c.items)
		nextFilter.Limit = 50
		page, err := b.store.Search(ctx, user, nextFilter)
		if err != nil {
			b.fail(ctx, chat, msg, err)
			return
		}
		c.items = append(c.items, page.Items...)
		c.total = page.Total
		b.mu.Lock()
		b.pages[p[1]] = c
		b.mu.Unlock()
	}
	if idx >= len(c.items) {
		idx = len(c.items) - 1
	}
	b.renderCard(ctx, chat, msg, p[1], idx)
}
func (b *Bot) renderCard(ctx context.Context, chat int64, msg int, token string, idx int) {
	b.mu.Lock()
	c := b.pages[token]
	b.mu.Unlock()
	l := c.items[idx]
	head := c.title
	if reason := c.reasons[l.ID]; reason != "" {
		head += fmt.Sprintf("\n\n🔥 <b>#%d</b>\n<i>%s</i>", idx+1, html.EscapeString(reason))
	}
	text := head + "\n\n" + card(l)
	prev := idx - 1
	if prev < 0 {
		prev = 0
	}
	next := idx + 1
	if next >= len(c.items) {
		if c.filter == nil || next >= c.total {
			next = len(c.items) - 1
		}
	}
	k := Markup{[][]Button{{urlb("Открыть Facebook", l.FacebookURL)}, {cb("❤️ Сохранить", fmt.Sprintf("save:%d", l.ID)), cb("🙈 Скрыть", fmt.Sprintf("hide:%d", l.ID)), cb("Подробнее", fmt.Sprintf("detail:%d", l.ID))}, {cb("◀️", fmt.Sprintf("page:%s:%d", token, prev)), cb(fmt.Sprintf("%d/%d", idx+1, c.total), "noop"), cb("▶️", fmt.Sprintf("page:%s:%d", token, next))}, {cb("← Меню", "menu")}}}
	b.editOrSend(ctx, chat, msg, text, k)
}
func card(l domain.Listing) string {
	var lines []string
	kind := map[string]string{"apartment": "Квартира", "house": "Дом", "room": "Комната", "studio": "Студия"}[l.PropertyType]
	var specs []string
	if kind != "" {
		specs = append(specs, kind)
	}
	if l.Bedrooms != nil && *l.Bedrooms > 0 {
		specs = append(specs, fmt.Sprintf("%d сп.", *l.Bedrooms))
	}
	if l.AreaM2 != nil {
		specs = append(specs, fmt.Sprintf("%.0f м²", *l.AreaM2))
	}
	if len(specs) > 0 {
		lines = append(lines, "🏠 <b>"+strings.Join(specs, " · ")+"</b>")
	}
	if l.District != "" || l.Address != "" {
		loc := l.District
		if l.Address != "" {
			loc += map[bool]string{true: " · ", false: ""}[loc != ""] + l.Address
		}
		lines = append(lines, "📍 "+html.EscapeString(loc))
	}
	if l.RentMin != nil {
		price := money(*l.RentMin)
		if l.RentMax != nil && *l.RentMax != *l.RentMin {
			price += "–" + money(*l.RentMax)
		}
		lines = append(lines, "\n💰 <b>"+price+" ₫ / мес.</b>")
	}
	if gov, ok := l.Utilities["government_rate"].(bool); ok && gov {
		lines = append(lines, "⚡ Электричество и вода: гос. тариф")
	}
	if l.Furnished != "" {
		lines = append(lines, "🪑 "+map[string]string{"full": "Полная мебель", "basic": "Базовая мебель", "none": "Без мебели"}[l.Furnished])
	}
	lines = append(lines, fmt.Sprintf("\n⭐ Deal score: <b>%.0f/100</b>", l.DealScore), "🕒 "+ago(l.PublishedAt))
	return strings.Join(lines, "\n")
}
func (b *Bot) showDetails(ctx context.Context, q *CallbackQuery, p []string) {
	if len(p) < 2 {
		return
	}
	id, _ := strconv.ParseInt(p[1], 10, 64)
	l, e := b.store.Listing(ctx, id)
	if e != nil {
		b.fail(ctx, q.Message.Chat.ID, q.Message.MessageID, e)
		return
	}
	text := card(l) + "\n\n<b>Исходное объявление</b>\n" + html.EscapeString(truncate(l.OriginalText, 1800))
	k := Markup{[][]Button{{urlb("Открыть Facebook", l.FacebookURL)}, {cb("← Назад", "menu")}}}
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, text, k)
}
func (b *Bot) listAction(ctx context.Context, q *CallbackQuery, save bool, p []string) {
	if len(p) < 2 {
		return
	}
	id, _ := strconv.ParseInt(p[1], 10, 64)
	var e error
	if save {
		e = b.store.Favorite(ctx, q.From.ID, id)
	} else {
		e = b.store.Hide(ctx, q.From.ID, id)
	}
	if e != nil {
		b.fail(ctx, q.Message.Chat.ID, q.Message.MessageID, e)
		return
	}
	_ = b.api.Answer(ctx, q.ID, map[bool]string{true: "Сохранено ❤️", false: "Скрыто"}[save])
}
func (b *Bot) runFavorites(ctx context.Context, q *CallbackQuery) {
	page, e := b.store.Favorites(ctx, q.From.ID, 50)
	if e != nil {
		b.fail(ctx, q.Message.Chat.ID, q.Message.MessageID, e)
		return
	}
	b.cacheAndShow(ctx, q.Message.Chat.ID, q.Message.MessageID, q.From.ID, page, len(page), "❤️ Избранное", nil, nil)
}
func (b *Bot) showMarket(ctx context.Context, q *CallbackQuery) {
	m, e := b.store.Market(ctx)
	if e != nil {
		b.fail(ctx, q.Message.Chat.ID, q.Message.MessageID, e)
		return
	}
	text := fmt.Sprintf("<b>📊 Рынок за 30 дней</b>\n\nОбъявлений с ценой: %d\nМедианная аренда: <b>%s ₫</b>\nМедиана за м²: %s ₫", m.Listings30d, money(m.MedianRent), money(m.MedianPriceM2))
	if len(m.Districts) > 0 {
		text += "\n\n<b>По районам</b>"
		for _, d := range m.Districts {
			text += fmt.Sprintf("\n%s · %d объявл. · %s ₫", html.EscapeString(d.District), d.Listings, money(d.MedianRent))
		}
	}
	if m.Listings30d < 20 {
		text += "\n\n<i>Выборка пока мала; статистика и deal score будут стабилизироваться по мере накопления данных.</i>"
	}
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, text, back("menu"))
}
func (b *Bot) showCollections(ctx context.Context, q *CallbackQuery) {
	k := Markup{[][]Button{{cb("Сегодня", "col:1"), cb("7 дней", "col:7"), cb("30 дней", "col:30")}, {cb("← Меню", "menu")}}}
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "<b>🔥 Подборки</b>\n\nЛокальный ranking отбирает лучшие варианты. Если LLM доступен, он только улучшает shortlist; при любой ошибке подборка остаётся доступной.", k)
}
func (b *Bot) runCollection(ctx context.Context, q *CallbackQuery, p []string) {
	days := 7
	if len(p) > 1 {
		days, _ = strconv.Atoi(p[1])
	}
	items, e := b.collections.Get(ctx, q.From.ID, days)
	if e != nil {
		b.fail(ctx, q.Message.Chat.ID, q.Message.MessageID, e)
		return
	}
	list := make([]domain.Listing, 0, len(items))
	reasons := map[int64]string{}
	for _, x := range items {
		list = append(list, x.Listing)
		reasons[x.ID] = x.Reason
	}
	b.cacheAndShow(ctx, q.Message.Chat.ID, q.Message.MessageID, q.From.ID, list, len(list), "🔥 Лучшие "+collections.Title(days), reasons, nil)
}

func (b *Bot) showAdmin(ctx context.Context, q *CallbackQuery) {
	k := Markup{[][]Button{{cb("Facebook Groups", "agroups")}, {cb("LLM", "llm")}, {cb("← Меню", "menu")}}}
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "<b>🛠 Админка</b>\n\nFacebook adapter: <code>"+html.EscapeString(b.fb.Name())+"</code>", k)
}
func (b *Bot) showGroups(ctx context.Context, q *CallbackQuery) {
	groups, e := b.store.Groups(ctx)
	if e != nil {
		b.fail(ctx, q.Message.Chat.ID, q.Message.MessageID, e)
		return
	}
	rows := make([][]Button, 0, len(groups)+3)
	for _, g := range groups {
		icon := "✅"
		if !g.Enabled {
			icon = "⏸"
		} else if g.ConsecutiveErrs > 0 {
			icon = "⚠️"
		}
		rows = append(rows, []Button{cb(icon+" "+truncate(g.Name, 28), fmt.Sprintf("ag:%d", g.ID))})
	}
	rows = append(rows, []Button{cb("➕ Добавить", "aadd")}, []Button{cb("⏱ Общий интервал", "apollall")}, []Button{cb("← Админка", "admin")})
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, fmt.Sprintf("<b>Facebook Groups</b>\nВсего: %d", len(groups)), Markup{rows})
}
func (b *Bot) showGroup(ctx context.Context, q *CallbackQuery, p []string) {
	if len(p) < 2 {
		return
	}
	id, _ := strconv.ParseInt(p[1], 10, 64)
	g, e := b.store.Group(ctx, id)
	if e != nil {
		b.fail(ctx, q.Message.Chat.ID, q.Message.MessageID, e)
		return
	}
	last := "ещё не было"
	if g.LastSuccessAt != nil {
		last = ago(*g.LastSuccessAt)
	}
	text := fmt.Sprintf("<b>%s</b>\n<code>%s</code>\n\nСтатус: %s\nИнтервал: %s\nПоследний успех: %s\nВсего постов: %d\nНовых за цикл: %d", html.EscapeString(g.Name), html.EscapeString(g.FacebookID), map[bool]string{true: "включена", false: "выключена"}[g.Enabled], g.PollingInterval, last, g.PostsTotal, g.NewPostsLastRun)
	if g.LastError != "" {
		text += "\nОшибка: <code>" + html.EscapeString(truncate(g.LastError, 300)) + "</code>"
	}
	k := Markup{[][]Button{{cb("🔄 Синхронизировать", fmt.Sprintf("async:%d", id)), cb("🔌 Проверить", fmt.Sprintf("acheck:%d", id))}, {cb("✏️ Имя", fmt.Sprintf("arename:%d", id)), cb("⏱ Интервал", fmt.Sprintf("apoll:%d", id))}, {cb(map[bool]string{true: "⏸ Выключить", false: "▶️ Включить"}[g.Enabled], fmt.Sprintf("atoggle:%d", id))}, {cb("🗑 Удалить", fmt.Sprintf("adel:%d:confirm", id))}, {cb("← Группы", "agroups")}}}
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, text, k)
}
func (b *Bot) toggleGroup(ctx context.Context, q *CallbackQuery, p []string) {
	if len(p) < 2 {
		return
	}
	id, _ := strconv.ParseInt(p[1], 10, 64)
	g, e := b.store.Group(ctx, id)
	if e == nil {
		e = b.store.UpdateGroup(ctx, id, g.Name, !g.Enabled, g.PollingInterval)
	}
	if e != nil {
		b.fail(ctx, q.Message.Chat.ID, q.Message.MessageID, e)
		return
	}
	b.showGroup(ctx, q, []string{"ag", p[1]})
}
func (b *Bot) deleteGroup(ctx context.Context, q *CallbackQuery, p []string) {
	if len(p) < 3 {
		return
	}
	id, _ := strconv.ParseInt(p[1], 10, 64)
	if p[2] == "confirm" {
		k := Markup{[][]Button{{cb("Да, удалить данные группы", fmt.Sprintf("adel:%d:yes", id))}, {cb("Отмена", fmt.Sprintf("ag:%d", id))}}}
		b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "Удалить группу и все её объявления? Действие необратимо.", k)
		return
	}
	if p[2] == "yes" {
		if e := b.store.DeleteGroup(ctx, id); e != nil {
			b.fail(ctx, q.Message.Chat.ID, q.Message.MessageID, e)
			return
		}
		b.showGroups(ctx, q)
	}
}
func (b *Bot) checkGroup(ctx context.Context, q *CallbackQuery, p []string) {
	if len(p) < 2 {
		return
	}
	id, _ := strconv.ParseInt(p[1], 10, 64)
	g, e := b.store.Group(ctx, id)
	if e == nil && g.FacebookID == "" {
		var name, url string
		g.FacebookID, name, url, e = b.fb.ResolveGroup(ctx, g.URL)
		if e == nil {
			e = b.store.ResolveGroup(ctx, g.ID, g.FacebookID, name, url)
		}
	}
	if e == nil {
		e = b.fb.Check(ctx, g.FacebookID)
	}
	if e != nil {
		b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "❌ Проверка не пройдена: <code>"+html.EscapeString(truncate(e.Error(), 350))+"</code>", back("ag:"+p[1]))
		return
	}
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "✅ Группа доступна, сессия Facebook работает.", back("ag:"+p[1]))
}

func (b *Bot) showLLM(ctx context.Context, q *CallbackQuery) {
	enabled := false
	_ = b.store.Setting(ctx, "llm.enabled", &enabled)
	text := fmt.Sprintf("<b>LLM</b>\nСтатус: %s\n\nLLM не участвует в обязательном парсинге. При сбое подборки автоматически используют deal score.", map[bool]string{true: "включён", false: "выключен"}[enabled])
	k := Markup{[][]Button{{cb(map[bool]string{true: "Выключить", false: "Включить"}[enabled], "llmset:enabled")}, {cb("Provider", "llmset:provider"), cb("Base URL", "llmset:base_url")}, {cb("Model", "llmset:model"), cb("Timeout", "llmset:timeout")}, {cb("Concurrency", "llmset:concurrency"), cb("API key", "llmset:api_key")}, {cb("Проверить подключение", "llmcheck")}, {cb("← Админка", "admin")}}}
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, text, k)
}
func (b *Bot) llmSet(ctx context.Context, q *CallbackQuery, p []string) {
	if len(p) < 2 {
		return
	}
	if p[1] == "enabled" {
		v := false
		_ = b.store.Setting(ctx, "llm.enabled", &v)
		_ = b.store.SetSetting(ctx, "llm.enabled", !v)
		b.showLLM(ctx, q)
		return
	}
	b.setState(q.From.ID, "llm."+p[1])
	hint := map[string]string{"provider": "openai или compatible", "base_url": "https://.../v1", "model": "имя модели", "timeout": "например 20s", "concurrency": "целое число", "api_key": "секретный ключ (сообщение будет обработано без повторного вывода)"}[p[1]]
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "Пришлите значение: "+hint, back("llm"))
}
func (b *Bot) checkLLM(ctx context.Context, q *CallbackQuery) {
	p := b.loadLLM(ctx)
	checkCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if e := p.Check(checkCtx); e != nil {
		b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "❌ Подключение не удалось: <code>"+html.EscapeString(truncate(e.Error(), 350))+"</code>", back("llm"))
		return
	}
	b.editOrSend(ctx, q.Message.Chat.ID, q.Message.MessageID, "✅ Подключение работает.", back("llm"))
}

func (b *Bot) handleState(ctx context.Context, m *Message, state string) {
	switch state {
	case "add_group":
		count := 0
		for _, line := range strings.Split(m.Text, "\n") {
			u := strings.TrimSpace(line)
			if u == "" {
				continue
			}
			name := u
			if i := strings.Index(u, "facebook.com/groups/"); i >= 0 {
				name = strings.Split(strings.TrimRight(u, "/"), "/")[len(strings.Split(strings.TrimRight(u, "/"), "/"))-1]
			}
			if _, e := b.store.AddGroup(ctx, "", u, name, b.defaultPoll); e == nil {
				count++
			}
		}
		b.send(ctx, m.Chat.ID, fmt.Sprintf("Добавлено групп: %d. Они будут проверены фоновым worker.", count), back("agroups"))
	case "poll_global":
		d, e := time.ParseDuration(strings.TrimSpace(m.Text))
		if e == nil && d >= 30*time.Second {
			e = b.store.SetAllPolling(ctx, d)
		}
		if e != nil || d < 30*time.Second {
			b.send(ctx, m.Chat.ID, "Некорректный интервал (минимум 30s).", back("agroups"))
		} else {
			b.send(ctx, m.Chat.ID, "Общий интервал обновлён.", back("agroups"))
		}
	default:
		if strings.HasPrefix(state, "rename:") {
			id, _ := strconv.ParseInt(strings.TrimPrefix(state, "rename:"), 10, 64)
			g, e := b.store.Group(ctx, id)
			if e == nil {
				e = b.store.UpdateGroup(ctx, id, strings.TrimSpace(m.Text), g.Enabled, g.PollingInterval)
			}
			if e != nil {
				b.send(ctx, m.Chat.ID, "Не удалось переименовать.", back("agroups"))
			} else {
				b.send(ctx, m.Chat.ID, "Название обновлено.", back("ag:"+strconv.FormatInt(id, 10)))
			}
			return
		}
		if strings.HasPrefix(state, "poll:") {
			id, _ := strconv.ParseInt(strings.TrimPrefix(state, "poll:"), 10, 64)
			d, e := time.ParseDuration(strings.TrimSpace(m.Text))
			g, gErr := b.store.Group(ctx, id)
			if e == nil && gErr == nil && d >= 30*time.Second {
				e = b.store.UpdateGroup(ctx, id, g.Name, g.Enabled, d)
			}
			if e != nil || gErr != nil || d < 30*time.Second {
				b.send(ctx, m.Chat.ID, "Некорректный интервал (минимум 30s).", back("ag:"+strconv.FormatInt(id, 10)))
			} else {
				b.send(ctx, m.Chat.ID, "Интервал обновлён.", back("ag:"+strconv.FormatInt(id, 10)))
			}
			return
		}
		if strings.HasPrefix(state, "llm.") {
			key := strings.TrimPrefix(state, "llm.")
			var e error
			if key == "api_key" {
				_ = b.api.Delete(ctx, m.Chat.ID, m.MessageID)
				e = b.store.SetSecret(ctx, b.cipher, "llm.api_key", m.Text)
			} else {
				var v any = m.Text
				if key == "concurrency" {
					v, _ = strconv.Atoi(strings.TrimSpace(m.Text))
				}
				e = b.store.SetSetting(ctx, "llm."+key, v)
			}
			if e != nil {
				b.send(ctx, m.Chat.ID, "Не удалось сохранить: "+html.EscapeString(e.Error()), back("llm"))
			} else {
				b.send(ctx, m.Chat.ID, "Сохранено. Секретные значения не выводятся обратно.", back("llm"))
			}
		}
	}
}
func (b *Bot) loadLLM(ctx context.Context) llm.Provider {
	enabled := false
	if b.store.Setting(ctx, "llm.enabled", &enabled) != nil || !enabled {
		return llm.Disabled{}
	}
	cfg := llm.Config{Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-5-mini", Timeout: 20 * time.Second, Concurrency: 2}
	_ = b.store.Setting(ctx, "llm.provider", &cfg.Provider)
	_ = b.store.Setting(ctx, "llm.base_url", &cfg.BaseURL)
	_ = b.store.Setting(ctx, "llm.model", &cfg.Model)
	var timeout string
	if b.store.Setting(ctx, "llm.timeout", &timeout) == nil {
		if d, e := time.ParseDuration(timeout); e == nil {
			cfg.Timeout = d
		}
	}
	_ = b.store.Setting(ctx, "llm.concurrency", &cfg.Concurrency)
	cfg.APIKey, _ = b.store.Secret(ctx, b.cipher, "llm.api_key")
	if cfg.APIKey == "" {
		return llm.Disabled{}
	}
	return llm.New(cfg)
}
func (b *Bot) Provider(ctx context.Context) llm.Provider { return b.loadLLM(ctx) }

func (b *Bot) setState(user int64, s string) { b.mu.Lock(); b.states[user] = s; b.mu.Unlock() }
func (b *Bot) editOrSend(ctx context.Context, chat int64, msg int, text string, k Markup) {
	var e error
	if msg > 0 {
		e = b.api.Edit(ctx, chat, msg, text, k)
	} else {
		_, e = b.api.Send(ctx, chat, text, k)
	}
	if e != nil && strings.Contains(e.Error(), "message is not modified") {
		return
	}
	if e != nil {
		b.log.Warn("telegram render", "error", e)
	}
}
func (b *Bot) send(ctx context.Context, chat int64, text string, k Markup) {
	_, e := b.api.Send(ctx, chat, text, k)
	if e != nil {
		b.log.Warn("telegram send", "error", e)
	}
}
func (b *Bot) fail(ctx context.Context, chat int64, msg int, e error) {
	b.log.Error("telegram action", "error", e)
	b.editOrSend(ctx, chat, msg, "Не удалось выполнить действие. Попробуйте ещё раз чуть позже.", back("menu"))
}
func cb(text, data string) Button  { return Button{Text: text, CallbackData: data} }
func urlb(text, url string) Button { return Button{Text: text, URL: url} }
func back(data string) Markup      { return Markup{[][]Button{{cb("← Назад", data)}}} }
func shortToken() string           { b := make([]byte, 4); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func money(v int64) string {
	s := strconv.FormatInt(v, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}
func ago(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "только что"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d мин. назад", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d ч. назад", int(d.Hours()))
	}
	return fmt.Sprintf("%d дн. назад", int(d.Hours()/24))
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
func filterSummary(f domain.SearchFilter) string {
	var x []string
	if f.RentMax != nil {
		x = append(x, "до "+money(*f.RentMax)+" ₫")
	}
	if f.Bedrooms != nil {
		if *f.Bedrooms == 0 {
			x = append(x, "studio")
		} else {
			x = append(x, fmt.Sprintf("%d сп.", *f.Bedrooms))
		}
	}
	if f.District != "" {
		x = append(x, f.District)
	}
	if f.PropertyType != "" {
		x = append(x, map[string]string{"apartment": "квартира", "house": "дом", "room": "комната"}[f.PropertyType])
	}
	if f.AreaMin != nil {
		x = append(x, fmt.Sprintf("от %.0f м²", *f.AreaMin))
	}
	if f.Furnished != "" {
		x = append(x, "с мебелью")
	}
	if f.NearBeach != nil && *f.NearBeach {
		x = append(x, "у моря")
	}
	if f.ForeignersAccepted != nil && *f.ForeignersAccepted {
		x = append(x, "для иностранцев")
	}
	if f.Sort == "new" {
		x = append(x, "сначала новые")
	}
	if len(x) == 0 {
		return "Фильтры не выбраны"
	}
	return "Выбрано: " + strings.Join(x, " · ")
}
