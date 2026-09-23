package domain

import "strings"

const (
	DistrictSonTra     = "Son Tra"
	DistrictNguHanhSon = "Ngu Hanh Son"
	DistrictHaiChau    = "Hai Chau"
	DistrictThanhKhe   = "Thanh Khe"
	DistrictLienChieu  = "Lien Chieu"
	DistrictCamLe      = "Cam Le"
	DistrictHoaVang    = "Hoa Vang"
	DistrictOther      = "Other"
	DistrictUnknown    = "Unknown"
)

var CanonicalDistricts = []string{DistrictSonTra, DistrictNguHanhSon, DistrictHaiChau, DistrictThanhKhe, DistrictLienChieu, DistrictCamLe, DistrictHoaVang, DistrictOther, DistrictUnknown}

var districtAliases = []struct {
	Canonical string
	Aliases   []string
}{
	{DistrictSonTra, []string{"sơn trà", "son tra"}},
	{DistrictNguHanhSon, []string{"ngũ hành sơn", "ngu hanh son", "mỹ an", "my an", "an thượng", "an thuong", "khu fpt", "fpt complex"}},
	{DistrictHaiChau, []string{"hải châu", "hai chau"}},
	{DistrictThanhKhe, []string{"thanh khê", "thanh khe"}},
	{DistrictLienChieu, []string{"liên chiểu", "lien chieu"}},
	{DistrictCamLe, []string{"cẩm lệ", "cam le"}},
	{DistrictHoaVang, []string{"hòa vang", "hoa vang"}},
}

func NormalizeDistrict(text string) string {
	s := strings.ToLower(strings.TrimSpace(text))
	for _, d := range districtAliases {
		if strings.EqualFold(strings.TrimSpace(text), d.Canonical) {
			return d.Canonical
		}
		for _, alias := range d.Aliases {
			if strings.Contains(s, alias) {
				return d.Canonical
			}
		}
	}
	if strings.EqualFold(s, DistrictOther) {
		return DistrictOther
	}
	if strings.EqualFold(s, DistrictUnknown) {
		return DistrictUnknown
	}
	return ""
}

func IsCanonicalDistrict(value string) bool {
	for _, district := range CanonicalDistricts {
		if value == district {
			return true
		}
	}
	return false
}

func DistrictLabel(value string) string {
	return map[string]string{DistrictSonTra: "Sơn Trà", DistrictNguHanhSon: "Ngũ Hành Sơn", DistrictHaiChau: "Hải Châu", DistrictThanhKhe: "Thanh Khê", DistrictLienChieu: "Liên Chiểu", DistrictCamLe: "Cẩm Lệ", DistrictHoaVang: "Hòa Vang", DistrictOther: "Другое", DistrictUnknown: "Неизвестно"}[value]
}
