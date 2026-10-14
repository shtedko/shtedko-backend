package main

import "testing"

func TestCategories(t *testing.T) {
	cases := []struct{ name, desc, want string }{
		{"МЕГЛЕ МЛЕКО 3.2% MM 1Л", "ТРАЈНО МЛЕКО", "milk"},
		{"МИЛКА ЧОКОЛАДА АЛПСКО МЛЕКО 250Г", "ЧОКОЛАДО", "other"},
		{"МОЈА КРАВИЦА ЧОКОЛАДНО МЛЕКО 250МЛ", "ЧОКОЛАДНО МЛЕКО", "milk"},
		{"ЛЕБ БЕЛ 500Г", "", "bread"},
		{"ЛЕБНИ ТРОШКИ 250Г", "", "other"},
		{"ПИЛЕШКИ СТЕК СВЕЖО 1КГ", "", "meat"},
		{"ТУНА ФИЛЕ 100Г", "", "other"},
		{"БРАШНО ТИП 400 1КГ", "", "flour"},
		{"ПОДРАВКА ОРИЗ 900Г", "", "rice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Category(tc.name, tc.desc); got != tc.want {
				t.Fatalf("category=%q expected %q", got, tc.want)
			}
		})
	}
}
func TestSizeAndComparison(t *testing.T) {
	sizes := []struct {
		in string
		v  float64
		u  string
	}{{"ТРАЈНО МЛЕКО 1Л", 1000, "ml"}, {"ЛЕБ 400ГР", 400, "g"}, {"ОРИЗ 2X250Г", 500, "g"}, {"ВОДА 1,5Л", 1500, "ml"}}
	for _, tc := range sizes {
		v, u := ExtractSize(tc.in)
		if v != tc.v || u != tc.u {
			t.Errorf("%q = %g%s; expected %g%s", tc.in, v, u, tc.v, tc.u)
		}
	}
	a := ComparisonKey("milk", "МЕГЛЕ МЛЕКО 3.2% 1Л", "ТРАЈНО МЛЕКО")
	b := ComparisonKey("milk", "ЗБРЕГОВ ТРАЈНО МЛЕКО 3.2% 2Л", "")
	if a == "" || a != b {
		t.Errorf("different size same milk spec should share candidate key: %q vs %q", a, b)
	}
	if ComparisonKey("milk", "АЛПСКО МЛЕКО 3.2% 1Л", "") != "" {
		t.Error("must not infer UHT processing")
	}
	if ComparisonKey("milk", "МЕГЛЕ МЛЕКО 2.8% 1Л", "ТРАЈНО МЛЕКО") == a {
		t.Error("different milk fat matches")
	}
}
func TestMoneyMinor(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{{"82.00", 8200}, {"82 ден.", 8200}, {"1.250,50 ден.", 125050}, {"1,250.50", 125050}, {"29", 2900}} {
		v, ok := MoneyMinor(tc.in)
		if !ok || v != tc.want {
			t.Errorf("%q got %d ok=%v wanted %d", tc.in, v, ok, tc.want)
		}
	}
}

func TestNoMilkFalsePositive(t *testing.T) {
	if got := Category("КРАВЈО СИРЕЊЕ 500Г", "СИРЕЊЕ ОД КРАВЈО МЛЕКО"); got != "cheese" {
		t.Fatalf("cheese should not be milk: %q", got)
	}
	if got := Category("ПИТА СО ЈУНЕШКО МЕСО 800Г", "ПЕЧИВО"); got == "meat" {
		t.Fatal("meat pie should not be classified as meat")
	}
}
