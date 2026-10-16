package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// KAM's public price-list landing page is currently not exposed as a readable
// product table in static HTML. kam_offers reads priced public promotional
// products from the KAM website instead. It is NEVER treated as a complete
// branch catalog; sync runs are labelled "partial".
var (
	kamJSONLDRe        = regexp.MustCompile(`(?is)<script\b[^>]*type\s*=\s*["']application/ld\+json["'][^>]*>(.*?)</script>`)
	kamHeadingRe       = regexp.MustCompile(`(?is)<h[34]\b[^>]*>(.*?)</h[34]\s*>`)
	kamDenarRe         = regexp.MustCompile(`(?i)(\d{1,6}(?:[.,]\d{1,2})?)\s*(?:ден\.?|mkd)`)
	kamSizeRe          = regexp.MustCompile(`(?i)\b\d+(?:[.,]\d+)?\s*(?:кг|кгр|гр|г|мл|л|kg|g|gr|ml|l)(?:\b|\s|<|$)`)
	kamHTMLAttrRe      = regexp.MustCompile(`(?i)\b(?:class|id)\s*=\s*["'][^"']*(?:product|proizvod|promotion|offer)[^"']*["']`)
	kamMarkupSectionRe = regexp.MustCompile(`(?is)<(?:div|article|li)\b[^>]*` + kamHTMLAttrRe.String() + `[^>]*>`)
)

func collectLDProducts(value interface{}, rows *[]ScrapedRow) {
	switch val := value.(type) {
	case []interface{}:
		for _, v := range val {
			collectLDProducts(v, rows)
		}
	case map[string]interface{}:
		typ := strings.ToLower(fmt.Sprint(val["@type"]))
		if typ == "product" || strings.Contains(typ, "product") {
			name := strings.TrimSpace(fmt.Sprint(val["name"]))
			if name != "" && name != "<nil>" {
				if price := ldPrice(val["offers"]); price != "" {
					*rows = append(*rows, ScrapedRow{Name: name, Price: price, Available: "Да"})
				}
			}
		}
		// JSON-LD often uses @graph, ItemList, or nested items.
		for k, v := range val {
			if k == "offers" {
				continue
			}
			if _, ok := v.(map[string]interface{}); ok {
				collectLDProducts(v, rows)
			}
			if _, ok := v.([]interface{}); ok {
				collectLDProducts(v, rows)
			}
		}
	}
}
func ldPrice(offers interface{}) string {
	switch v := offers.(type) {
	case []interface{}:
		for _, o := range v {
			if p := ldPrice(o); p != "" {
				return p
			}
		}
	case map[string]interface{}:
		curr := strings.ToUpper(strings.TrimSpace(fmt.Sprint(v["priceCurrency"])))
		if curr != "MKD" {
			return ""
		}
		p := strings.TrimSpace(fmt.Sprint(v["price"]))
		if n, ok := MoneyMinor(p); ok && n > 0 {
			return p + " MKD"
		}
	}
	return ""
}

// ParseKAMOffers supports structured Product/Offer JSON-LD, with an HTML
// heading fallback for the publicly visible KAM offer cards. It requires
// explicit denar labels, so unrelated dates and percentage discounts cannot
// accidentally become prices. No price means no product.
func ParseKAMOffers(body []byte) ([]ScrapedRow, error) {
	html := string(body)
	rows := []ScrapedRow{}
	for _, script := range kamJSONLDRe.FindAllStringSubmatch(html, -1) {
		var content interface{}
		if json.Unmarshal([]byte(script[1]), &content) == nil {
			collectLDProducts(content, &rows)
		}
	}
	// Some offer sections are not marked up as JSON-LD. Bound the search by
	// adjacent product headings, and require currency explicitly displayed.
	// Restrict to markup near product/offer anchors to exclude editorial titles.
	headings := kamHeadingRe.FindAllStringSubmatchIndex(html, -1)
	for i, h := range headings {
		name := CleanHTML(html[h[2]:h[3]])
		if len([]rune(name)) < 4 || len([]rune(name)) > 160 {
			continue
		}
		start := h[0] - 500
		if start < 0 {
			start = 0
		}
		if !kamHTMLAttrRe.MatchString(html[start:h[0]]) {
			continue
		}
		end := h[1] + 1400
		if i+1 < len(headings) && headings[i+1][0] < end {
			end = headings[i+1][0]
		}
		if end > len(html) {
			end = len(html)
		}
		// Use the title plus its own following content, not the next card.
		chunk := CleanHTML(html[h[1]:end])
		if !kamSizeRe.MatchString(chunk) {
			continue
		}
		vals := kamDenarRe.FindAllStringSubmatch(chunk, -1)
		if len(vals) == 0 {
			continue
		}
		price := vals[len(vals)-1][1] + " ден."
		if cents, ok := MoneyMinor(price); !ok || cents <= 0 {
			continue
		}
		// For discounted listings the last displayed checkout price is the
		// discounted price; first is the regular price when higher.
		regular := ""
		if len(vals) >= 2 {
			old, oldok := MoneyMinor(vals[0][1])
			now, _ := MoneyMinor(price)
			if oldok && old > now {
				regular = vals[0][1] + " ден."
			}
		}
		size := kamSizeRe.FindString(chunk)
		rows = append(rows, ScrapedRow{Name: strings.TrimSpace(name + " " + size), Price: price, Regular: regular, Available: ""})
	}
	dedup := map[string]ScrapedRow{}
	for _, r := range rows {
		key := Normalize(r.Name)
		if key != "" {
			dedup[key] = r
		}
	}
	result := make([]ScrapedRow, 0, len(dedup))
	// Stable output helps tests and makes syncs deterministic.
	for _, r := range rows {
		key := Normalize(r.Name)
		if got, ok := dedup[key]; ok {
			result = append(result, got)
			delete(dedup, key)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("KAM has no parsable priced offers: site markup may have changed; existing database kept")
	}
	return result, nil
}
func (c *Collector) collectKAMOffers(ctx context.Context, s Source) ([]ScrapedRow, string, error) {
	body, err := c.Fetcher.Get(ctx, s.URL)
	if err != nil {
		return nil, "", err
	}
	rows, err := ParseKAMOffers(body)
	if err != nil {
		return nil, "", err
	}
	return rows, "", nil
}
