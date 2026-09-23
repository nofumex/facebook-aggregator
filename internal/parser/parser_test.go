package parser

import (
	"github.com/egori/facebook-aggregator/internal/domain"
	"testing"
	"time"
)

func parse(t *testing.T, text string) domain.Listing {
	t.Helper()
	return New().Parse(domain.FacebookPost{ID: "1", Text: text, PublishedAt: time.Now()}, 1, "g")
}
func TestVietnameseListing(t *testing.T) {
	l := parse(t, "Cho thuê căn hộ 2PN 50m² Sơn Trà, giá thuê 5tr5/tháng. Cọc 1 tháng. Full nội thất, ban công, máy giặt riêng, cách biển 500m. Điện nước giá nhà nước, nhận người nước ngoài")
	if l.RentMin == nil || *l.RentMin != 5_500_000 {
		t.Fatalf("rent=%v", l.RentMin)
	}
	if l.Bedrooms == nil || *l.Bedrooms != 2 {
		t.Fatal("bedrooms")
	}
	if l.AreaM2 == nil || *l.AreaM2 != 50 {
		t.Fatal("area")
	}
	if l.District != "Son Tra" {
		t.Fatal(l.District)
	}
	if l.BeachDistanceM == nil || *l.BeachDistanceM != 500 {
		t.Fatal("beach")
	}
	if !l.Amenities["private_washing_machine"] {
		t.Fatal("washing")
	}
}
func TestPrices(t *testing.T) {
	cases := map[string]int64{"5tr": 5_000_000, "5tr500": 5_500_000, "5 triệu 500": 5_500_000, "5.5tr": 5_500_000, "5,5 triệu": 5_500_000, "5000k": 5_000_000, "5 million": 5_000_000, "12 000 000 đ": 12_000_000, "7 củ": 7_000_000}
	for s, want := range cases {
		l := parse(t, "rent "+s+" / month")
		var got int64
		if l.RentMin != nil {
			got = *l.RentMin
		}
		if got != want {
			t.Errorf("%s got %d want %d raw=%v", s, got, want, l.RawValues)
		}
	}
}

func TestPriceRangeWithUnitsOnBothSides(t *testing.T) {
	l := parse(t, "Cho thuê căn hộ 2PN, giá 6tr - 7tr/tháng")
	if l.RentMin == nil || l.RentMax == nil || *l.RentMin != 6_000_000 || *l.RentMax != 7_000_000 {
		t.Fatalf("range=%v-%v raw=%v", l.RentMin, l.RentMax, l.RawValues)
	}
}
func TestFeeNotRent(t *testing.T) {
	l := parse(t, "Căn hộ 1PN giá 6tr/tháng, điện 3500đ, nước 100k, wifi 100k, cọc 6tr")
	if l.RentMin == nil || *l.RentMin != 6_000_000 {
		t.Fatalf("rent %v", l.RentMin)
	}
	if l.DepositAmount == nil || *l.DepositAmount != 6_000_000 {
		t.Fatal("deposit")
	}
}
func TestAmbiguousStillSaved(t *testing.T) {
	l := parse(t, "Nice apartment in My An. Inbox for price")
	if l.OriginalText == "" || l.District != "Ngu Hanh Son" {
		t.Fatalf("%+v", l)
	}
}
func TestParseNaturalSearch(t *testing.T) {
	f := ParseSearch("2 спальни son tra до 6 млн")
	if f.Bedrooms == nil || *f.Bedrooms != 2 || f.District != "Son Tra" || f.RentMax == nil || *f.RentMax != 6_000_000 || f.Query != "" {
		t.Fatalf("%+v", f)
	}
}

func TestDistrictAliasesShareCanonicalEnum(t *testing.T) {
	for _, q := range []string{"Sơn Trà", "son tra"} {
		if got := ParseSearch(q).District; got != "Son Tra" {
			t.Fatalf("%q -> %q", q, got)
		}
	}
	for _, q := range []string{"Ngũ Hành Sơn", "My An", "Khu FPT"} {
		if got := ParseSearch(q).District; got != "Ngu Hanh Son" {
			t.Fatalf("%q -> %q", q, got)
		}
	}
}

func TestCleanForExtractionPreservesVisibleFacts(t *testing.T) {
	in := "[🏠](https://static.xx.fbcdn.net/icon.png) Studio 4TR5 [📍](https://facebook.com/x) Khu FPT +84901234567"
	want := "🏠 Studio 4TR5 📍 Khu FPT +84901234567"
	if got := CleanForExtraction(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
