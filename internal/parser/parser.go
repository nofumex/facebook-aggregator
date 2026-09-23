package parser

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/egori/facebook-aggregator/internal/domain"
)

type Parser struct{}

func New() *Parser { return &Parser{} }

var (
	priceRE         = regexp.MustCompile(`(?i)\b\d{1,3}\s*(?:tr|triệu|million|m|млн)\s*\d{1,3}(?:\s*(?:k|nghìn))?\b|\b\d{1,3}(?:[.,]\d{1,3})?\s*(?:tr|triệu|million|m|млн|củ|chai)\s*(?:-|–|—|đến|to)\s*\d{1,3}(?:[.,]\d{1,3})?\s*(?:tr|triệu|million|m|млн|củ|chai)(?:\b|\s|/|$)|\b\d{1,3}(?:[.,]\d{1,3})?\s*(?:-|–|—|đến|to)\s*\d{1,3}(?:[.,]\d{1,3})?\s*(?:tr|triệu|million|m|млн|củ|chai)(?:\b|\s|/|$)|\b(?:\d{1,3}(?:[.,\s]\d{3}){2,}|\d{1,9}(?:[.,]\d{1,3})?)\s*(?:tr|triệu|million|m|млн|củ|chai|k|nghìn|vnd|vnđ|₫|đ)(?:\b|\s|/|$)`)
	bedRE           = regexp.MustCompile(`(?i)\b([1-9])\s*(?:pn|p\.ngủ|phòng\s*ngủ|br|bdr|bed(?:room)?s?|спальн(?:я|и|ь|ей)?)\b`)
	areaRE          = regexp.MustCompile(`(?i)\b(\d{1,3}(?:[.,]\d)?)\s*(?:m2|m²|m\^2|sqm|sq\.?\s*m)(?:\b|\s|$)`)
	distanceRE      = regexp.MustCompile(`(?i)(\d{1,4})\s*(m|km)\s*(?:tới|đến|from|to)?\s*(?:biển|beach)`)
	distanceAfterRE = regexp.MustCompile(`(?i)(?:biển|beach)\D{0,20}(\d{1,4})\s*(m|km)\b`)
	leaseRE         = regexp.MustCompile(`(?i)(?:hợp đồng|lease|thuê)\s*(?:từ|min(?:imum)?|:)?\s*(\d{1,2})\s*(?:tháng|months?)`)
	phoneRE         = regexp.MustCompile(`\b(?:\+?84|0)[\s.-]?(?:\d[\s.-]?){8,10}\b`)
	searchBedRU     = regexp.MustCompile(`(?i)([1-9])\s*спальн(?:я|и|ь|ей)?`)
	markdownLinkRE  = regexp.MustCompile(`\[([^\]]+)\]\(https?://[^)]+\)`)
)

type moneyHit struct {
	value      int64
	raw        string
	start, end int
}

// CleanForExtraction removes Facebook's technical markdown links while
// preserving their human-visible label, numbers, phones and location text.
func CleanForExtraction(s string) string {
	s = markdownLinkRE.ReplaceAllString(s, "$1")
	return normalize(s)
}

func (p *Parser) Parse(post domain.FacebookPost, groupID int64, groupName string) domain.Listing {
	t := normalize(post.Text)
	lower := strings.ToLower(t)
	l := domain.Listing{FacebookPostID: post.ID, FacebookURL: post.URL, GroupID: groupID, GroupName: groupName, AuthorName: post.AuthorName, OriginalText: post.Text, PublishedAt: post.PublishedAt, Currency: "VND", Amenities: map[string]bool{}, Utilities: map[string]any{}, RawValues: map[string]any{}, Confidence: domain.Confidence{}, MediaURLs: post.MediaURLs}

	hits := extractMoney(t)
	l.RawValues["money_mentions"] = rawMoney(hits)
	var rents []int64
	for _, h := range hits {
		ctx := clauseAround(lower, h.start, h.end)
		switch {
		case containsAny(ctx, "cọc", "deposit"):
			l.DepositAmount = ptr(h.value)
			l.Confidence["deposit"] = .86
		case containsAny(ctx, "điện", "electric"):
			l.Utilities["electricity_vnd"] = h.value
		case containsAny(ctx, "nước", "water"):
			l.Utilities["water_vnd"] = h.value
		case containsAny(ctx, "wifi", "internet"):
			l.Utilities["wifi_vnd"] = h.value
		case containsAny(ctx, "dịch vụ", "service", "phí quản lý"):
			l.Utilities["service_vnd"] = h.value
		case containsAny(ctx, "parking", "gửi xe"):
			l.Utilities["parking_vnd"] = h.value
		case containsAny(ctx, "foreign", "nước ngoài", "người nn"):
			l.ForeignerPrice = ptr(h.value)
			l.Confidence["foreigner_price"] = .78
		default:
			if h.value >= 1_000_000 && h.value <= 200_000_000 {
				rents = append(rents, h.value)
			}
		}
	}
	if len(rents) > 0 {
		min, max := minmax(rents)
		l.RentMin = ptr(min)
		l.RentMax = ptr(max)
		l.Confidence["price"] = priceConfidence(lower, len(rents))
	}
	if strings.Contains(lower, "giá cũ") && len(rents) > 1 {
		l.RawValues["possible_old_and_new_prices"] = rents
	}
	if m := bedRE.FindStringSubmatch(lower); len(m) > 0 {
		n, _ := strconv.Atoi(m[1])
		l.Bedrooms = &n
		l.Confidence["bedrooms"] = .96
	}
	if containsAny(lower, "studio", "căn hộ st", "căn studio") {
		z := 0
		l.Bedrooms = &z
		l.PropertyType = "studio"
		l.Confidence["bedrooms"] = .98
	}
	if m := areaRE.FindStringSubmatch(lower); len(m) > 0 {
		v, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		if v >= 8 && v <= 1000 {
			l.AreaM2 = &v
			l.Confidence["area_m2"] = .95
			l.RawValues["area"] = m[0]
		}
	}
	if l.PropertyType == "" {
		l.PropertyType = propertyType(lower)
		if l.PropertyType != "" {
			l.Confidence["property_type"] = .83
		}
	}
	l.District = district(lower)
	if l.District != "" {
		l.Confidence["district"] = .9
	}
	l.NearBeach = boolMatch(lower, []string{"gần biển", "near beach", "close to beach", "walking to beach", "cách biển"}, []string{"xa biển", "far from beach"})
	if l.NearBeach != nil {
		l.Confidence["near_beach"] = .84
	}
	if m := distanceRE.FindStringSubmatch(lower); len(m) > 0 {
		v, _ := strconv.ParseFloat(m[1], 64)
		if m[2] == "km" {
			v *= 1000
		}
		n := int(v)
		l.BeachDistanceM = &n
		b := n <= 1500
		l.NearBeach = &b
		l.Confidence["beach_distance_m"] = .94
	} else if m := distanceAfterRE.FindStringSubmatch(lower); len(m) > 0 {
		v, _ := strconv.ParseFloat(m[1], 64)
		if m[2] == "km" {
			v *= 1000
		}
		n := int(v)
		l.BeachDistanceM = &n
		near := n <= 1500
		l.NearBeach = &near
		l.Confidence["beach_distance_m"] = .92
	}
	l.Furnished = furnishing(lower)
	if l.Furnished != "" {
		l.Confidence["furnished"] = .82
	}
	amenities := map[string][]string{"balcony": {"ban công", "balcony"}, "elevator": {"thang máy", "elevator", "lift"}, "washing_machine": {"máy giặt", "washing machine"}, "private_washing_machine": {"máy giặt riêng", "private washing"}, "kitchen": {"bếp", "kitchen"}, "pool": {"hồ bơi", "bể bơi", "pool"}, "gym": {"phòng gym", "gym"}, "parking": {"đậu xe", "đỗ xe", "parking", "gửi xe"}, "wifi": {"wifi", "internet"}, "air_conditioning": {"điều hòa", "máy lạnh", "aircon", "a/c"}}
	for k, words := range amenities {
		if containsAny(lower, words...) {
			l.Amenities[k] = true
		}
	}
	l.PetsAllowed = boolMatch(lower, []string{"pet friendly", "pets allowed", "cho nuôi thú", "được nuôi pet"}, []string{"no pet", "no pets", "không pet", "không nuôi thú"})
	l.ForeignersAccepted = boolMatch(lower, []string{"foreigners welcome", "foreigner friendly", "accept foreigners", "người nước ngoài", "khách tây"}, []string{"không nhận người nước ngoài", "no foreigners", "chỉ người việt"})
	l.TemporaryResidence = boolMatch(lower, []string{"đăng ký tạm trú", "temporary residence", "khai báo tạm trú"}, []string{"không đăng ký tạm trú"})
	if m := leaseRE.FindStringSubmatch(lower); len(m) > 0 {
		n, _ := strconv.Atoi(m[1])
		l.LeaseMonths = &n
		l.Confidence["lease_months"] = .9
	}
	if containsAny(lower, "giá điện nhà nước", "điện nước giá nhà nước", "government rate", "state rate") {
		l.Utilities["government_rate"] = true
	}
	l.EstimatedMonthlyTotalMin, l.EstimatedMonthlyTotalMax = estimateTotal(l)
	return l
}

func extractMoney(s string) []moneyHit {
	var out []moneyHit
	for _, idx := range priceRE.FindAllStringSubmatchIndex(s, -1) {
		raw := s[idx[0]:idx[1]]
		v1, v2 := parseMoneyRaw(raw)
		if v1 > 0 {
			out = append(out, moneyHit{v1, raw, idx[0], idx[1]})
		}
		if v2 > 0 && v2 != v1 {
			out = append(out, moneyHit{v2, raw, idx[0], idx[1]})
		}
	}
	return out
}

func parseMoneyRaw(raw string) (int64, int64) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "triệu", "tr")
	s = strings.ReplaceAll(s, "million", "m")
	s = strings.ReplaceAll(s, "млн", "m")
	s = strings.ReplaceAll(s, "củ", "m")
	s = strings.ReplaceAll(s, "chai", "m")
	s = strings.ReplaceAll(s, "vnđ", "vnd")
	s = strings.ReplaceAll(s, "₫", "đ")
	s = strings.ReplaceAll(s, "đến", "-")
	s = strings.ReplaceAll(s, "–", "-")
	s = strings.ReplaceAll(s, "—", "-")
	if m := regexp.MustCompile(`^(\d{1,3})\s*(?:tr|m)\s*(\d{1,3})(?:\s*(?:k|nghìn))?$`).FindStringSubmatch(s); len(m) > 0 {
		a, _ := strconv.ParseInt(m[1], 10, 64)
		b, _ := strconv.ParseInt(m[2], 10, 64)
		mul := int64(100000)
		if len(m[2]) == 1 {
			mul = 100000
		}
		if len(m[2]) == 3 {
			mul = 1000
		}
		return a*1_000_000 + b*mul, 0
	}
	unit := ""
	for _, u := range []string{"vnd", "tr", "m", "k", "nghìn", "đ"} {
		if strings.Contains(s, u) {
			unit = u
			break
		}
	}
	clean := regexp.MustCompile(`[^0-9,.-]`).ReplaceAllString(strings.ReplaceAll(s, " ", ""), "")
	parts := strings.Split(clean, "-")
	vals := make([]int64, 0, 2)
	for _, x := range parts {
		if x == "" {
			continue
		}
		x = strings.Trim(x, ".,")
		var val float64
		if unit == "tr" || unit == "m" {
			val, _ = strconv.ParseFloat(strings.ReplaceAll(x, ",", "."), 64)
			val *= 1_000_000
		} else if unit == "k" || unit == "nghìn" {
			val, _ = strconv.ParseFloat(strings.ReplaceAll(x, ",", "."), 64)
			val *= 1000
		} else {
			normalized := strings.ReplaceAll(strings.ReplaceAll(x, ".", ""), ",", "")
			val, _ = strconv.ParseFloat(normalized, 64)
		}
		vals = append(vals, int64(math.Round(val)))
	}
	if len(vals) == 0 {
		return 0, 0
	}
	if len(vals) == 1 {
		return vals[0], 0
	}
	return vals[0], vals[1]
}

func propertyType(s string) string {
	switch {
	case containsAny(s, "nhà nguyên căn", "whole house", "entire house", "villa", "biệt thự", "townhouse"):
		return "house"
	case containsAny(s, "phòng trọ", "phòng cho thuê", "room for rent"):
		return "room"
	case containsAny(s, "căn hộ", "chung cư", "apartment", "condo", "apt"):
		return "apartment"
	}
	return ""
}
func district(s string) string {
	districts := []struct {
		name  string
		words []string
	}{{"Son Tra", []string{"sơn trà", "son tra"}}, {"Ngu Hanh Son", []string{"ngũ hành sơn", "ngu hanh son", "mỹ an", "my an", "an thượng", "an thuong", "khu fpt", "fpt complex"}}, {"Hai Chau", []string{"hải châu", "hai chau"}}, {"Thanh Khe", []string{"thanh khê", "thanh khe"}}, {"Lien Chieu", []string{"liên chiểu", "lien chieu"}}, {"Cam Le", []string{"cẩm lệ", "cam le"}}, {"Hoa Vang", []string{"hòa vang", "hoa vang"}}}
	for _, d := range districts {
		if containsAny(s, d.words...) {
			return d.name
		}
	}
	return ""
}
func furnishing(s string) string {
	switch {
	case containsAny(s, "full nội thất", "đầy đủ nội thất", "fully furnished", "full furniture"):
		return "full"
	case containsAny(s, "cơ bản", "basic furniture", "partly furnished"):
		return "partial"
	case containsAny(s, "không nội thất", "unfurnished", "no furniture"):
		return "none"
	}
	return ""
}
func estimateTotal(l domain.Listing) (*int64, *int64) {
	if l.RentMin == nil {
		return nil, nil
	}
	var extra int64
	for k, v := range l.Utilities {
		if k == "electricity_vnd" || k == "water_vnd" {
			continue
		}
		if n, ok := v.(int64); ok && n < 2_000_000 {
			extra += n
		}
	}
	a, b := *l.RentMin+extra, *l.RentMax+extra
	return &a, &b
}
func priceConfidence(s string, n int) float64 {
	c := .62
	if containsAny(s, "giá thuê", "cho thuê", "rent", "monthly", "per month", "/tháng", "/ tháng", "tháng") {
		c += .25
	} else if containsAny(s, "giá", "price") {
		c += .15
	}
	if n > 2 {
		c -= .12
	}
	return math.Max(.35, math.Min(.96, c))
}
func boolMatch(s string, yes, no []string) *bool {
	if containsAny(s, no...) {
		v := false
		return &v
	}
	if containsAny(s, yes...) {
		v := true
		return &v
	}
	return nil
}
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
func contextAround(s string, start, end, n int) string {
	a := start - n
	if a < 0 {
		a = 0
	}
	b := end + n
	if b > len(s) {
		b = len(s)
	}
	return s[a:b]
}
func clauseAround(s string, start, end int) string {
	a, b := start, end
	for a > 0 && !strings.ContainsRune("\n,;.!|", rune(s[a-1])) {
		a--
	}
	for b < len(s) && !strings.ContainsRune("\n,;.!|", rune(s[b])) {
		b++
	}
	return s[a:b]
}
func normalize(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) }), " ")
}
func rawMoney(h []moneyHit) []string {
	r := make([]string, 0, len(h))
	for _, x := range h {
		r = append(r, x.raw)
	}
	return r
}
func minmax(v []int64) (int64, int64) {
	a, b := v[0], v[0]
	for _, x := range v[1:] {
		if x < a {
			a = x
		}
		if x > b {
			b = x
		}
	}
	return a, b
}
func ptr[T any](v T) *T { return &v }

// ParseSearch is deliberately deterministic and conservative; unrecognized
// words stay in Query for PostgreSQL full-text matching.
func ParseSearch(q string) domain.SearchFilter {
	f := domain.SearchFilter{Query: strings.TrimSpace(q), Limit: 10, Sort: "score"}
	lower := strings.ToLower(q)
	recognized := false
	if m := bedRE.FindStringSubmatch(lower); len(m) > 0 {
		n, _ := strconv.Atoi(m[1])
		f.Bedrooms = &n
		recognized = true
	}
	if f.Bedrooms == nil {
		if m := searchBedRU.FindStringSubmatch(lower); len(m) > 0 {
			n, _ := strconv.Atoi(m[1])
			f.Bedrooms = &n
			recognized = true
		}
	}
	if strings.Contains(lower, "studio") {
		n := 0
		f.Bedrooms = &n
		recognized = true
	}
	if d := district(lower); d != "" {
		f.District = d
		recognized = true
	}
	if containsAny(lower, "near beach", "gần biển", "у моря", "рядом с морем") {
		v := true
		f.NearBeach = &v
		recognized = true
	}
	if containsAny(lower, "иностран", "foreigner", "nước ngoài") {
		v := true
		f.ForeignersAccepted = &v
		recognized = true
	}
	h := extractMoney(q)
	if len(h) > 0 {
		v := h[len(h)-1].value
		if containsAny(lower, "до ", "under", "max", "dưới") {
			f.RentMax = &v
		} else {
			f.RentMax = &v
		}
		recognized = true
	}
	if containsAny(lower, "сегодня", "today", "hôm nay") {
		t := time.Now().Add(-24 * time.Hour)
		f.FreshAfter = &t
		recognized = true
	}
	if recognized {
		f.Query = ""
	}
	return f
}

func RedactContact(s string) string {
	return phoneRE.ReplaceAllString(s, "[телефон скрыт]")
}
