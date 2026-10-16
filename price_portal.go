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

// PricePortal is the same publicly visible, paginated Macedonian price-list
// implementation used by Stokomak and Zito. This is a live HTML source,
// NOT a CSV feed and NOT an affiliate/third-party price aggregator.
//
// Examples observed on their public sites:
//
//	https://stokomak.proverkanaceni.mk/?org=5&page=1&perPage=100&search=
//	https://zito.proverkanaceni.mk/?org=13&perPage=20&search=
//
// Only commit a snapshot when the server's reported total has been fetched.
func (c *Collector) collectPricePortal(ctx context.Context, s Source) ([]ScrapedRow, string, error) {
	if strings.TrimSpace(s.Org) == "" {
		return nil, "", errors.New("price_portal requires org (retail store ID)")
	}
	if _, e := strconv.Atoi(s.Org); e != nil {
		return nil, "", fmt.Errorf("invalid numeric store org %q", s.Org)
	}
	base, e := url.Parse(s.URL)
	if e != nil {
		return nil, "", e
	}
	if base.Scheme != "https" && base.Scheme != "http" {
		return nil, "", errors.New("price portal URL must be HTTP(S)")
	}
	maxPages := c.MaxPages
	if maxPages < 1 {
		maxPages = 500
	}
	seen := map[string]bool{}
	var rows []ScrapedRow
	var updated string
	total := 0
	// Portal supports perPage=100; reduces load versus its 20-row default.
	// We also verify totals and recognize truncated / repeated pages.
	for n := 1; n <= maxPages; n++ {
		if e := ctx.Err(); e != nil {
			return nil, "", e
		}
		dest := *base
		q := dest.Query()
		q.Set("org", s.Org)
		q.Set("page", strconv.Itoa(n))
		q.Set("perPage", "100")
		q.Set("search", "")
		dest.RawQuery = q.Encode()
		raw, e := c.Fetcher.Get(ctx, dest.String())
		if e != nil {
			return nil, "", fmt.Errorf("portal %s page %d: %w", s.Org, n, e)
		}
		p, e := ParsePriceTable(raw)
		if e != nil {
			return nil, "", fmt.Errorf("portal %s page %d: %w", s.Org, n, e)
		}
		if p.Total <= 0 {
			return nil, "", fmt.Errorf("portal %s page %d did not report catalog total", s.Org, n)
		}
		if total == 0 {
			total = p.Total
			updated = p.UpdatedAt
		}
		// Changing totals mid-crawl may shift page boundaries, causing missing
		// or duplicated products. Fail safely and retry on the next cron run.
		if p.Total != total {
			return nil, "", fmt.Errorf("portal %s total changed during crawl: %d -> %d", s.Org, total, p.Total)
		}
		fp := p.Rows[0].Name + "|" + p.Rows[len(p.Rows)-1].Name
		if seen[fp] {
			return nil, "", fmt.Errorf("portal %s page %d repeated a previous page", s.Org, n)
		}
		seen[fp] = true
		if updated == "" {
			updated = p.UpdatedAt
		}
		rows = append(rows, p.Rows...)
		if len(rows) >= total {
			if len(rows) != total {
				return nil, "", fmt.Errorf("portal %s reported %d, fetched %d rows", s.Org, total, len(rows))
			}
			return rows, updated, nil
		}
	}
	return nil, "", fmt.Errorf("portal %s hit max_pages=%d with %d/%d products", s.Org, maxPages, len(rows), total)
}

var (
	portalSelectRe = regexp.MustCompile(`(?is)<select\b([^>]*)>(.*?)</select\s*>`)
	portalOptionRe = regexp.MustCompile(`(?is)<option\b[^>]*\bvalue\s*=\s*["']?(\d{1,8})["']?[^>]*>(.*?)</option\s*>`)
)

// Discover store IDs from the retailer's own HTML <select> menu. Restrict
// discovery to its store selector (org) to avoid treating perPage selectors
// as shops. Unreadable dynamic menus use an explicitly configured fallback
// org; in that case the directory status is "partial".
func PortalStoreOptions(body []byte) map[string]string {
	out := map[string]string{}
	selects := portalSelectRe.FindAllStringSubmatch(string(body), -1)
	for _, s := range selects {
		attrs := strings.ToLower(s[1])
		if !strings.Contains(attrs, "org") && !strings.Contains(attrs, "market") && !strings.Contains(attrs, "store") && !strings.Contains(attrs, "prodav") {
			continue
		}
		for _, opt := range portalOptionRe.FindAllStringSubmatch(s[2], -1) {
			label := CleanHTML(opt[2])
			id := opt[1]
			if label == "" || strings.Contains(strings.ToLower(label), "изберете") || strings.Contains(strings.ToLower(label), "select") {
				continue
			}
			out[id] = label
		}
	}
	return out
}

func (c *Collector) syncPricePortalDirectory(ctx context.Context, s Source) (bool, error) {
	body, err := c.Fetcher.Get(ctx, s.URL)
	if err != nil {
		return false, err
	}
	options := PortalStoreOptions(body)
	fullDiscovery := len(options) > 0
	if len(options) == 0 && s.Org != "" {
		options[s.Org] = "Store " + s.Org + " (configured fallback)"
	}
	if len(options) == 0 {
		return false, errors.New("store options not found; configure a known org fallback for partial collection")
	}
	ids := make([]string, 0, len(options))
	for id := range options {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	failures := []string{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		storeURL, e := url.Parse(s.URL)
		if e != nil {
			return false, e
		}
		query := storeURL.Query()
		query.Set("org", id)
		storeURL.RawQuery = query.Encode()
		store := Source{ID: s.Market + "-" + id, Market: s.Market, Name: strings.ToUpper(s.Market) + " " + options[id], Type: "price_portal", URL: storeURL.String(), Org: id}
		if err := c.syncSource(ctx, store); err != nil {
			failures = append(failures, store.ID+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fullDiscovery, fmt.Errorf("%d/%d %s stores failed: %s", len(failures), len(ids), s.Market, strings.Join(failures, "; "))
	}
	return fullDiscovery, nil
}
