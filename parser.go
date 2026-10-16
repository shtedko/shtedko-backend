package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var (
	tableRe  = regexp.MustCompile(`(?is)<table\b[^>]*>(.*?)</table\s*>`)
	rowRe    = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr\s*>`)
	cellRe   = regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]\s*>`)
	tagRe    = regexp.MustCompile(`(?is)<[^>]+>`)
	scriptRe = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)\s*>`)
	spaceRe  = regexp.MustCompile(`\s+`)
	totalRe  = regexp.MustCompile(`(?i)\bod\s+(\d+)\s+artikli\b`)
	dateRe   = regexp.MustCompile(`\b(\d{1,2}[./]\d{1,2}[./]\d{4})\b`)
)

type ScrapedRow struct{ Name, Description, Price, Regular, Available, UpdatedAt string }
type Page struct {
	Rows      []ScrapedRow
	Total     int
	UpdatedAt string
}

func CleanHTML(s string) string {
	s = scriptRe.ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(s, " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(tagRe.ReplaceAllString(s, " ")), " "))
}
func ParsePriceTable(body []byte) (Page, error) {
	content := string(body)
	whole := CleanHTML(content)
	result := Page{}
	if m := totalRe.FindStringSubmatch(Normalize(whole)); len(m) > 1 {
		result.Total, _ = strconv.Atoi(m[1])
	}
	if m := dateRe.FindStringSubmatch(whole); len(m) > 1 {
		result.UpdatedAt = m[1]
	}
	for _, table := range tableRe.FindAllStringSubmatch(content, -1) {
		headerIdx := -1
		candidate := []ScrapedRow{}
		for ri, tr := range rowRe.FindAllStringSubmatch(table[1], -1) {
			if ri > 200000 {
				return Page{}, errors.New("too many table rows")
			}
			rawCells := cellRe.FindAllStringSubmatch(tr[1], -1)
			cells := []string{}
			for _, m := range rawCells {
				cells = append(cells, CleanHTML(m[1]))
			}
			if len(cells) < 2 {
				continue
			}
			first := Normalize(cells[0])
			second := Normalize(cells[1])
			if (strings.Contains(first, "naziv") || strings.Contains(first, "ime na proizvod")) && strings.Contains(second, "cena") {
				headerIdx = ri
				continue
			}
			if headerIdx < 0 || ri <= headerIdx {
				continue
			}
			if len(cells) < 5 {
				continue
			}
			// Price tables published by Tinex and Ramstore share these first six columns.
			if strings.TrimSpace(cells[0]) == "" || strings.TrimSpace(cells[1]) == "" {
				continue
			}
			if _, valid := MoneyMinor(cells[1]); !valid {
				continue
			}
			row := ScrapedRow{Name: cells[0], Price: cells[1], Description: cells[3], Available: cells[4]}
			if len(cells) > 5 {
				row.Regular = cells[5]
			}
			candidate = append(candidate, row)
		}
		if len(candidate) > len(result.Rows) {
			result.Rows = candidate
		}
	}
	if len(result.Rows) == 0 {
		return result, fmt.Errorf("no rows in recognizable product price table (HTML may be JS-rendered or changed)")
	}
	return result, nil
}

// ReadCSV lets any retailer export a full catalog with the same interface.
// The scheduled sync re-reads the file; replacing a feed is atomic on success.
func ReadCSV(r io.Reader) ([]ScrapedRow, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	headers, err := cr.Read()
	if err != nil {
		return nil, err
	}
	fields := map[string]int{}
	for i, h := range headers {
		fields[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	if _, ok := fields["name"]; !ok {
		return nil, errors.New("CSV missing name column")
	}
	if _, ok := fields["price_mkd"]; !ok {
		if _, found := fields["price"]; !found {
			return nil, errors.New("CSV missing price_mkd or price column")
		}
	}
	var out []ScrapedRow
	get := func(record []string, names ...string) string {
		for _, name := range names {
			if i, ok := fields[name]; ok && i < len(record) {
				return strings.TrimSpace(record[i])
			}
		}
		return ""
	}
	for line := 2; ; line++ {
		row, e := cr.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("CSV line %d: %w", line, e)
		}
		name := get(row, "name")
		if name == "" {
			continue
		}
		price := get(row, "price_mkd", "price")
		if _, ok := MoneyMinor(price); !ok {
			return nil, fmt.Errorf("CSV line %d has invalid price %q", line, price)
		}
		out = append(out, ScrapedRow{Name: name, Description: get(row, "description"), Price: price, Regular: get(row, "regular_price_mkd", "regular_price"), Available: get(row, "available"), UpdatedAt: get(row, "source_updated_at", "updated_at", "price_date")})
	}
	if len(out) == 0 {
		return nil, errors.New("CSV has no products")
	}
	return out, nil
}
