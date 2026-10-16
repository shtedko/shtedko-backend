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

// Kipper publishes branch pages with an item/retail-price/availability table.
// Some pages currently expose only the table header to ordinary HTTP clients.
// Such pages are not considered a successful product crawl: no invented prices,
// no deactivation of an earlier successful store snapshot.
var (
	kipperStoreNo = regexp.MustCompile(`(?i)(?:kipper|кипп?ер)\s*[#№]?\s*0*(\d{1,3})\b`)
	kipperDate    = regexp.MustCompile(`(?i)(?:датум и време на последно ажурирање на цените|last updated)\s*:\s*(\d{1,2}\s+[a-z]+\s+\d{4}(?:\s*[-–]\s*\d{1,2}:\d{2})?|\d{1,2}[./]\d{1,2}[./]\d{4}(?:\s+\d{1,2}:\d{2})?)`)
)

func ParseKipperStoreTable(body []byte) (Page, error) {
	html := string(body)
	result := Page{}
	if m := kipperDate.FindStringSubmatch(strings.ToLower(CleanHTML(html))); len(m) > 1 {
		result.UpdatedAt = m[1]
	}
	for _, table := range tableRe.FindAllStringSubmatch(html, -1) {
		var candidates []ScrapedRow
		var indexes map[string]int
		for _, tr := range rowRe.FindAllStringSubmatch(table[1], -1) {
			cells := []string{}
			for _, c := range cellRe.FindAllStringSubmatch(tr[1], -1) {
				cells = append(cells, CleanHTML(c[1]))
			}
			if len(cells) < 2 {
				continue
			}
			if indexes == nil {
				cols := map[string]int{}
				for i, cell := range cells {
					value := Normalize(cell)
					switch {
					case strings.Contains(value, "ime na artikal") || strings.Contains(value, "naziv na stoka") || strings.Contains(value, "ime na proizvod"):
						cols["name"] = i
					case strings.Contains(value, "prodazhna cena") || strings.Contains(value, "prodazna cena") || strings.Contains(value, "selling price"):
						cols["price"] = i
					case strings.Contains(value, "opis na stoka") || strings.Contains(value, "description"):
						cols["description"] = i
					case strings.Contains(value, "dostapnost") || strings.Contains(value, "availability"):
						cols["available"] = i
					case strings.Contains(value, "redovna cena") || strings.Contains(value, "regular price"):
						cols["regular"] = i
					}
				}
				if _, ok := cols["name"]; ok {
					if _, yes := cols["price"]; yes {
						indexes = cols
					}
				}
				continue
			}
			read := func(key string) string {
				i, ok := indexes[key]
				if !ok || i >= len(cells) {
					return ""
				}
				return cells[i]
			}
			name, price := read("name"), read("price")
			if name == "" {
				continue
			}
			if _, ok := MoneyMinor(price); !ok {
				continue
			}
			regular := read("regular")
			if regular != "" {
				if _, ok := MoneyMinor(regular); !ok {
					regular = ""
				}
			}
			candidates = append(candidates, ScrapedRow{Name: name, Price: price, Description: read("description"), Available: read("available"), Regular: regular})
		}
		if len(candidates) > len(result.Rows) {
			result.Rows = candidates
		}
	}
	if len(result.Rows) == 0 {
		return Page{}, errors.New("Kipper branch price table has no visible priced rows (possibly JavaScript-loaded); preserved previous snapshot")
	}
	return result, nil
}

func (c *Collector) collectKipperStore(ctx context.Context, s Source) ([]ScrapedRow, string, error) {
	body, err := c.Fetcher.Get(ctx, s.URL)
	if err != nil {
		return nil, "", err
	}
	parsed, err := ParseKipperStoreTable(body)
	if err != nil {
		return nil, "", err
	}
	return parsed.Rows, parsed.UpdatedAt, nil
}

// Kipper lists stores both in a paginated WordPress market archive and in its
// newer market directory. Discover only links whose title contains a Kipper
// branch number; do not guess branch URLs from integers.
func KipperStoreLinks(body []byte, base *url.URL) map[string]Source {
	out := map[string]Source{}
	for _, anchor := range hrefHTML.FindAllStringSubmatch(string(body), -1) {
		title := CleanHTML(anchor[2])
		number := kipperStoreNo.FindStringSubmatch(title)
		if len(number) < 2 {
			continue
		}
		n, e := strconv.Atoi(number[1])
		if e != nil || n <= 0 {
			continue
		}
		ref, e := url.Parse(strings.TrimSpace(anchor[1]))
		if e != nil {
			continue
		}
		resolved := base.ResolveReference(ref)
		if resolved.Scheme != base.Scheme || !strings.EqualFold(resolved.Hostname(), base.Hostname()) {
			continue
		}
		if !strings.HasPrefix(resolved.EscapedPath(), "/mk/") {
			continue
		}
		// Store paths may have slugs in Cyrillic; use the verified link verbatim.
		if strings.Contains(resolved.Path, "/category/") || strings.Contains(resolved.Path, "/marketet/") || strings.Contains(resolved.Path, "/shop/") {
			continue
		}
		resolved.RawQuery = ""
		resolved.Fragment = ""
		id := fmt.Sprintf("kipper-%03d", n)
		out[id] = Source{ID: id, Market: "kipper", Name: title, Type: "kipper_store", URL: resolved.String()}
	}
	return out
}

func kipperNextDirectoryPage(body []byte, now, origin *url.URL, pageNum int) string {
	target := strings.TrimRight(origin.Path, "/") + "/page/" + strconv.Itoa(pageNum+1) + "/"
	for _, a := range hrefHTML.FindAllStringSubmatch(string(body), -1) {
		v, e := url.Parse(a[1])
		if e != nil {
			continue
		}
		u := now.ResolveReference(v)
		if u.Hostname() == origin.Hostname() && u.Path == target {
			return u.String()
		}
	}
	return ""
}

func (c *Collector) syncKipperDirectory(ctx context.Context, s Source) (bool, error) {
	origin, e := url.Parse(s.URL)
	if e != nil {
		return false, e
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false, errors.New("Kipper directory must be HTTP(S)")
	}
	maxPages := c.MaxPages
	if maxPages < 1 {
		maxPages = 100
	}
	next := origin.String()
	visited := map[string]bool{}
	sources := map[string]Source{}
	// WordPress category archives do not prove that they enumerate every branch.
	full := false
	for pageNum := 1; pageNum <= maxPages; pageNum++ {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if visited[next] {
			return false, errors.New("Kipper directory pagination repeated")
		}
		visited[next] = true
		pageURL, e := url.Parse(next)
		if e != nil {
			return false, e
		}
		data, e := c.Fetcher.Get(ctx, next)
		if e != nil {
			return false, fmt.Errorf("Kipper directory page %d: %w", pageNum, e)
		}
		stores := KipperStoreLinks(data, pageURL)
		if len(stores) == 0 {
			return false, fmt.Errorf("Kipper directory page %d has no recognizable store links", pageNum)
		}
		for id, source := range stores {
			sources[id] = source
		}
		upcoming := kipperNextDirectoryPage(data, pageURL, origin, pageNum)
		if upcoming == "" {
			if strings.Contains(strings.ToLower(CleanHTML(string(data))), "load more") {
				full = false
			}
			break
		}
		if pageNum == maxPages {
			return false, fmt.Errorf("Kipper directory exceeded max_pages=%d", maxPages)
		}
		next = upcoming
	}
	keys := make([]string, 0, len(sources))
	for id := range sources {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	failures := []string{}
	for _, id := range keys {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err := c.syncSource(ctx, sources[id]); err != nil {
			failures = append(failures, id+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		// Never claim directory success if even one branch has no visible data.
		limit := min(4, len(failures))
		return full, fmt.Errorf("Kipper branch price collection failed for %d/%d discovered branches (first %d: %s)", len(failures), len(keys), limit, strings.Join(failures[:limit], "; "))
	}
	return full, nil
}
