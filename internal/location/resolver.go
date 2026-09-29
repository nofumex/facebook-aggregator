package location

import (
	_ "embed"
	"encoding/json"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Rule struct {
	Canonical string
	Zone      string
	Aliases   []string
	Oceanus   bool
	Near      bool
}

type Result struct {
	Canonical   string
	Zone        string
	IsOceanus   bool
	NearOceanus bool
}

type Resolver struct{ rules []Rule }

//go:embed nha_trang.json
var nhaTrangData []byte

func NhaTrang() Resolver {
	var rules []Rule
	_ = json.Unmarshal(nhaTrangData, &rules)
	return Resolver{rules: rules}
}

func (r Resolver) Resolve(values ...string) Result {
	text := fold(strings.Join(values, " "))
	for _, rule := range r.rules {
		for _, alias := range rule.Aliases {
			if strings.Contains(text, fold(alias)) {
				return Result{Canonical: rule.Canonical, Zone: rule.Zone, IsOceanus: rule.Oceanus, NearOceanus: rule.Near}
			}
		}
	}
	return Result{}
}

func fold(s string) string {
	s = strings.ToLower(norm.NFD.String(s))
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		switch r {
		case 'đ':
			return 'd'
		}
		return r
	}, s)
}
