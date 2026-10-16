package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Verified page layout: https://pricelist.vero.com.mk/89_1.html
// Columns are name, selling price, unit price, available, description, regular price, ...
func ParseVeroPriceTable(body []byte) (Page, error) {
	content := string(body)
	result := Page{}
	if m := regexp.MustCompile(`(?i)Последно\s+ажурирање:\s*(\d{1,2}[./]\d{1,2}[./]\d{4}(?:\s+\d{1,2}:\d{2})?)`).FindStringSubmatch(CleanHTML(content)); len(m) > 1 {
		result.UpdatedAt = m[1]
	}
	for _, table := range tableRe.FindAllStringSubmatch(content, -1) {
		var rows []ScrapedRow
		header := false
		for _, tr := range rowRe.FindAllStringSubmatch(table[1], -1) {
			var cells []string
			for _, match := range cellRe.FindAllStringSubmatch(tr[1], -1) {
				cells = append(cells, CleanHTML(match[1]))
			}
			if len(cells) < 2 {
				continue
			}
			first := Normalize(cells[0])
			if strings.Contains(first, "naziv na stoka") || strings.Contains(first, "naziv na proizvod") {
				header = true
				continue
			}
			if !header || len(cells) < 6 {
				continue
			}
			if cells[0] == "" {
				continue
			}
			if _, ok := MoneyMinor(cells[1]); !ok {
				continue
			}
			rows = append(rows, ScrapedRow{Name: cells[0], Price: cells[1], Description: cells[4], Available: cells[3], Regular: cells[5]})
		}
		if len(rows) > len(result.Rows) {
			result.Rows = rows
		}
	}
	if len(result.Rows) == 0 {
		return Page{}, errors.New("Vero price table has no recognized rows (markup changed?)")
	}
	return result, nil
}

var veroPageURL = regexp.MustCompile(`(?i)(?:^|/)(\d+)_(\d+)\.html$`)
var hrefHTML = regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a\s*>`)

func (c *Collector) collectVero(ctx context.Context, s Source) ([]ScrapedRow, string, error) {
	first, err := url.Parse(s.URL)
	if err != nil {
		return nil, "", err
	}
	match := veroPageURL.FindStringSubmatch(first.Path)
	if len(match) == 0 {
		return nil, "", fmt.Errorf("Vero URL must use /store_page.html: %s", s.URL)
	}
	store := match[1]
	maxPages := c.MaxPages
	if maxPages < 1 {
		maxPages = 500
	}
	var all []ScrapedRow
	var updated string
	seen := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		dest := *first
		dest.Path = fmt.Sprintf("/%s_%d.html", store, page)
		body, err := c.Fetcher.Get(ctx, dest.String())
		if err != nil {
			return nil, "", fmt.Errorf("Vero page %d: %w", page, err)
		}
		result, err := ParseVeroPriceTable(body)
		if err != nil {
			return nil, "", fmt.Errorf("Vero page %d: %w", page, err)
		}
		if updated == "" {
			updated = result.UpdatedAt
		}
		fingerprint := result.Rows[0].Name + "|" + result.Rows[len(result.Rows)-1].Name
		if seen[fingerprint] {
			return nil, "", fmt.Errorf("repeating Vero price page %d", page)
		}
		seen[fingerprint] = true
		all = append(all, result.Rows...)
		// Only stop if page N+1 is not linked from the current page.
		next := fmt.Sprintf("/%s_%d.html", store, page+1)
		foundNext := false
		for _, a := range hrefHTML.FindAllStringSubmatch(string(body), -1) {
			u, e := url.Parse(a[1])
			if e != nil {
				continue
			}
			resolved := dest.ResolveReference(u)
			if resolved.Host == dest.Host && resolved.Path == next {
				foundNext = true
				break
			}
		}
		if !foundNext {
			return all, updated, nil
		}
	}
	return nil, "", fmt.Errorf("Vero reached max_pages=%d without end-of-pagination evidence", maxPages)
}

func (c *Collector) syncVeroDirectory(ctx context.Context, s Source) error {
	body, err := c.Fetcher.Get(ctx, s.URL)
	if err != nil {
		return err
	}
	base, err := url.Parse(s.URL)
	if err != nil {
		return err
	}
	stores := map[string]Source{}
	for _, a := range hrefHTML.FindAllStringSubmatch(string(body), -1) {
		parsed, err := url.Parse(a[1])
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(parsed)
		if resolved.Host != base.Host {
			continue
		}
		m := veroPageURL.FindStringSubmatch(resolved.Path)
		if len(m) == 0 || m[2] != "1" {
			continue
		}
		label := CleanHTML(a[2])
		if label == "" {
			label = "Vero " + m[1]
		}
		market := "vero"
		if strings.Contains(strings.ToLower(label), "џамбо") || strings.Contains(strings.ToLower(label), "jumbo") {
			market = "jumbo"
		}
		id := market + "-" + m[1]
		stores[id] = Source{ID: id, Market: market, Name: label, Type: "vero_html", URL: resolved.String()}
	}
	if len(stores) == 0 {
		return fmt.Errorf("no Vero store links in %s", s.URL)
	}
	ids := make([]string, 0, len(stores))
	for id := range stores {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var failures []string
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.syncSource(ctx, stores[id]); err != nil {
			failures = append(failures, id+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d/%d Vero stores failed: %s", len(failures), len(ids), strings.Join(failures, "; "))
	}
	return nil
}

func veroPageNumber(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	m := veroPageURL.FindStringSubmatch(u.Path)
	if len(m) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}
