package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var transliteration = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'ѓ': "gj", 'е': "e", 'ж': "zh", 'з': "z", 'ѕ': "dz", 'и': "i", 'ј': "j", 'к': "k", 'ќ': "kj", 'л': "l", 'љ': "lj", 'м': "m", 'н': "n", 'њ': "nj", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "h", 'ц': "c", 'ч': "ch", 'џ': "dzh", 'ш': "sh",
}

func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if repl, ok := transliteration[r]; ok {
			b.WriteString(repl)
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '%' || r == '.' || r == ',' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
func hasToken(s string, tokens ...string) bool {
	s = " " + s + " "
	for _, t := range tokens {
		if strings.Contains(s, " "+t+" ") {
			return true
		}
	}
	return false
}
func hasFragment(s string, tokens ...string) bool {
	for _, t := range tokens {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

var sizeRx = regexp.MustCompile(`(?i)(?:(\d+)\s*[xх×*]\s*)?(\d+(?:[.,]\d+)?)\s*(kg|kgr|kilogram|gr|g|gram|ml|l|lit|litar|pcs|parchinja|parce)\b`)
var percentRx = regexp.MustCompile(`\b(\d+(?:[.,]\d+)?)\s*%`)

func ExtractSize(original string) (float64, string) {
	m := sizeRx.FindStringSubmatch(Normalize(original))
	if len(m) == 0 {
		return 0, ""
	}
	n, _ := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", "."), 64)
	mult := 1.
	if m[1] != "" {
		mult, _ = strconv.ParseFloat(m[1], 64)
	}
	unit := strings.ToLower(m[3])
	n *= mult
	switch unit {
	case "kg", "kgr", "kilogram":
		return n * 1000, "g"
	case "l", "lit", "litar":
		return n * 1000, "ml"
	case "gr", "gram":
		return n, "g"
	case "pcs", "parchinja", "parce":
		return n, "pcs"
	default:
		return n, unit
	}
}
func Category(name, desc string) string {
	s := Normalize(name + " " + desc)
	// Beware "Milka" chocolate and milk-based desserts: match actual word mleko.
	if hasToken(s, "mleko", "milk", "mleka") && !hasFragment(Normalize(name), "sirenje", "kashkaval", "mocarela", "yogurt", "jogurt") && !hasFragment(s, "mlechno chokolado", "mlechen desert", "milk chocolate") && !(hasFragment(s, "chokolada", "chokolado") && !hasFragment(s, "chokoladno mleko", "chocolate milk")) {
		return "milk"
	}
	if hasToken(s, "leb", "bread", "baget", "baguette", "pogacha", "tost") && !hasFragment(s, "lebni troshki", "breadcrumbs") {
		return "bread"
	}
	if hasToken(s, "pileshko", "pileshki", "pile", "chicken", "svinsko", "svinski", "svinski", "junesko", "juneshko", "govedsko", "beef", "pork", "meso", "stek", "file") {
		// A 'tuna fillet' or meat-flavored snack is not fresh meat.
		if !hasFragment(s, "tuna", "tunjevina", "riba", "fish", "pate", "pashteta", "vkus", "aroma", "kroasan", "supa", "pita", "burek", "pizza", "lazanja", "pasta") {
			return "meat"
		}
	}
	if hasToken(s, "jajca", "jajce", "eggs", "egg") {
		return "eggs"
	}
	if hasToken(s, "jogurt", "yogurt") {
		return "yogurt"
	}
	if hasToken(s, "sirenje", "cheese", "kashkaval", "mocarela") {
		return "cheese"
	}
	if hasToken(s, "brashno", "brasno", "flour") {
		return "flour"
	}
	if hasToken(s, "oriz", "rice") && !hasFragment(s, "orizovi galeti", "rice cake") {
		return "rice"
	}
	if hasToken(s, "maslo", "oil", "zejtin") && !hasFragment(s, "puter", "butter", "maslinovo maslo za telo") {
		return "oil"
	}
	return "other"
}

// ComparisonKey is only a candidate equivalence bucket. It is never proof of identical SKU.
// Do not assign a key unless the critical characteristics are present.
func ComparisonKey(category, name, desc string) string {
	s := Normalize(name + " " + desc)
	switch category {
	case "milk":
		m := percentRx.FindStringSubmatch(s)
		if len(m) < 2 {
			return ""
		}
		fat := strings.ReplaceAll(m[1], ",", ".")
		process := ""
		if hasToken(s, "trajno", "uht", "longlife") {
			process = "uht"
		}
		if hasToken(s, "svezho", "svezhe", "fresh", "pasterizirano", "pasteurized") {
			if process != "" {
				return ""
			}
			process = "fresh"
		}
		if process == "" {
			return ""
		}
		animal := "unspecified"
		if hasFragment(s, "kozjo", "kozje", "goat") {
			animal = "goat"
		} else if hasFragment(s, "ovcho", "ovche", "sheep") {
			animal = "sheep"
		} else if hasFragment(s, "kravjo", "kravje", "cow") {
			animal = "cow"
		}
		flavor := "plain"
		if hasFragment(s, "chokoladno", "kakao", "chocolate") {
			flavor = "chocolate"
		} else if hasFragment(s, "jagoda", "strawberry") {
			flavor = "strawberry"
		} else if hasFragment(s, "vanila", "vanilla") {
			flavor = "vanilla"
		}
		lactose := "normal"
		if hasFragment(s, "bez laktoza", "lactose free") {
			lactose = "lactose_free"
		}
		return strings.Join([]string{"milk", animal, process, fat, flavor, lactose}, "|")
	case "bread":
		flour := ""
		if hasFragment(s, "integralen", "polnozrnest", "wholegrain", "whole grain") {
			flour = "wholegrain"
		} else if hasFragment(s, "bel leb", "bel ", "white") {
			flour = "white"
		} else if hasFragment(s, "rzh", "rye") {
			flour = "rye"
		}
		if flour == "" {
			return ""
		}
		return "bread|" + flour
	case "meat":
		animal := ""
		if hasFragment(s, "pilesh", "pile", "chicken") {
			animal = "chicken"
		} else if hasFragment(s, "svinsk", "pork") {
			animal = "pork"
		} else if hasFragment(s, "junes", "junesh", "goved", "beef") {
			animal = "beef"
		}
		cut := ""
		if hasFragment(s, "gradi", "gradi", "breast", "file", "stek") {
			cut = "breast"
		} else if hasFragment(s, "vrat", "neck") {
			cut = "neck"
		} else if hasFragment(s, "but", "thigh") {
			cut = "thigh"
		}
		state := ""
		if hasFragment(s, "zamrznat", "frozen") {
			state = "frozen"
		} else if hasFragment(s, "svezh", "fresh") {
			state = "fresh"
		}
		if animal == "" || cut == "" || state == "" {
			return ""
		}
		return "meat|" + animal + "|" + cut + "|" + state
	}
	return ""
}
func MakeProduct(meta StoreMeta, name, description, rawPrice, rawRegular, availability, sourceDate string) (Product, error) {
	price, ok := MoneyMinor(rawPrice)
	if !ok {
		return Product{}, fmt.Errorf("invalid price %q", rawPrice)
	}
	norm := Normalize(name)
	if norm == "" {
		return Product{}, fmt.Errorf("empty product name")
	}
	idHash := sha256.Sum256([]byte(meta.ID + "\x00" + norm))
	id := hex.EncodeToString(idHash[:16])
	category := Category(name, description)
	size, unit := ExtractSize(name)
	p := Product{ID: id, Name: strings.TrimSpace(name), Description: strings.TrimSpace(description), Category: category, ComparisonKey: ComparisonKey(category, name, description), SourceURL: meta.URL, SizeValue: size, SizeUnit: unit, PriceMinor: price, SourceUpdatedAt: sourceDate}
	if rawRegular != "" {
		if rp, ok := MoneyMinor(rawRegular); ok {
			p.RegularPriceMinor = &rp
		}
	}
	av := Normalize(availability)
	if hasToken(av, "da", "yes", "true", "1") {
		t := true
		p.Available = &t
	} else if hasToken(av, "ne", "no", "false", "0") {
		f := false
		p.Available = &f
	}
	return p, nil
}

// Exact MKD cents: price extraction is distinct from package-size parsing.
func MoneyMinor(s string) (int64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ден.", "")
	s = strings.ReplaceAll(s, "ден", "")
	s = strings.ReplaceAll(s, "mkd", "")
	var b strings.Builder
	started := false
	for _, r := range s {
		if r >= '0' && r <= '9' || r == '.' || r == ',' {
			b.WriteRune(r)
			started = true
		} else if started {
			break
		}
	}
	n := b.String()
	if n == "" {
		return 0, false
	}
	if strings.Contains(n, ",") && strings.Contains(n, ".") {
		if strings.LastIndex(n, ",") > strings.LastIndex(n, ".") {
			n = strings.ReplaceAll(n, ".", "")
			n = strings.ReplaceAll(n, ",", ".")
		} else {
			n = strings.ReplaceAll(n, ",", "")
		}
	} else if strings.Contains(n, ",") {
		parts := strings.Split(n, ",")
		if len(parts[len(parts)-1]) <= 2 {
			n = strings.ReplaceAll(n, ",", ".")
		} else {
			n = strings.ReplaceAll(n, ",", "")
		}
	} else if strings.Count(n, ".") > 0 {
		parts := strings.Split(n, ".")
		if len(parts[len(parts)-1]) == 3 {
			n = strings.ReplaceAll(n, ".", "")
		}
	}
	v, err := strconv.ParseFloat(n, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return int64(v*100 + 0.5), true
}
