package parser

import (
	"github.com/nofumex/telegram-aggregator/internal/domain"
	"testing"
)

func TestParseSearchCityZoneAndPrice(t *testing.T) {
	f := ParseSearch("Nha Trang север 1 спальня до 7 млн")
	if f.City != domain.CityNhaTrang || f.Zone != domain.ZoneNorth || f.Bedrooms == nil || *f.Bedrooms != 1 || f.RentMax == nil || *f.RentMax != 7_000_000 {
		t.Fatalf("%+v", f)
	}
}
