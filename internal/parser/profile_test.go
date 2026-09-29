package parser

import (
	"github.com/nofumex/telegram-aggregator/internal/domain"
	"testing"
)

func TestParseOnlyWithStoredChannelProfile(t *testing.T) {
	p := domain.ChannelParsingProfile{PostTypeIndicators: []string{"RENT"}, FieldRules: map[string]domain.FieldRule{"rent_vnd": {Patterns: []string{`(?i)price:\s*(?P<value>[0-9.,]+)\s*tr`}, ValueGroup: "value", Unit: "million_vnd"}, "bedrooms": {Patterns: []string{`(?i)(?P<value>\d+)BR`}, ValueGroup: "value", ValueType: "integer"}, "property_type": {Patterns: []string{`(?i)(?P<value>apartment)`}, ValueGroup: "value", Mappings: []domain.ValueMapping{{From: "apartment", To: "apartment"}}}}}
	x, e := ParseWithProfile(p, domain.TelegramPost{Text: "RENT apartment 1BR Price: 6.5 tr"}, domain.CityNhaTrang)
	if e != nil || x.RentVND == nil || *x.RentVND != 6_500_000 || x.Bedrooms == nil || *x.Bedrooms != 1 || x.ParsedFields < 3 {
		t.Fatalf("%+v %v", x, e)
	}
}
func TestProfileParserHasNoFallback(t *testing.T) {
	p := domain.ChannelParsingProfile{FieldRules: map[string]domain.FieldRule{"rent_vnd": {Patterns: []string{`PRICE=(?P<value>\d+)`}, ValueGroup: "value", Unit: "vnd"}}}
	x, e := ParseWithProfile(p, domain.TelegramPost{Text: "cheap apartment 7 million"}, domain.CityNhaTrang)
	if e != nil || x.RentVND != nil || x.ParsedFields != 0 {
		t.Fatalf("fallback detected: %+v %v", x, e)
	}
}

func TestProfileParserNormalizesVNDThousands(t *testing.T) {
	p := domain.ChannelParsingProfile{FieldRules: map[string]domain.FieldRule{"rent_vnd": {Patterns: []string{`PRICE=(?P<value>[0-9.]+)`}, ValueGroup: "value", Unit: "vnd"}}}
	x, e := ParseWithProfile(p, domain.TelegramPost{Text: "PRICE=7.000.000"}, domain.CityNhaTrang)
	if e != nil || x.RentVND == nil || *x.RentVND != 7_000_000 {
		t.Fatalf("%+v %v", x, e)
	}
}
