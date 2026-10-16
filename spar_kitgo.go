package main

// SPAR and Super Kit-Go: discover stores from the retailers' published links,
// parse priced rows conservatively, and optionally render JS-driven listings
// through the local Playwright bridge. No inferred endpoint or invented prices.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	sparStorePath = regexp.MustCompile(`^/pricelist/(\d{1,5})/?$`)
	kitgoStoreID  = regexp.MustCompile(`(?i)(?:[?&](?:id|market|market_id|store|store_id)=|/(?:market|cenovnik|pricelist|download)/)(\d{1,5})(?:\b|/|$)`)
	shopHeading   = regexp.MustCompile(`(?is)<h[1-5]\b[^>]*>(.*?)</h[1-5]>`)
	sparCount     = regexp.MustCompile(`(?i)(?:маркет|market)\s+\d+\s+(\d[\d,.]*)\s+(?:производи|products)`)
	sparUpdated   = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:\s+\d{2}:\d{2}:\d{2})?\b`)
)

// Discover only real linked stores on the retailer's own origin. Labels are
// informative, not proof that similarly numbered SPAR and Kit-Go stores differ.
func MarketDirectoryLinks(body []byte, base *url.URL, market string) map[string]Source {
	stores := map[string]Source{}
	html := string(body)
	anchors := hrefHTML.FindAllStringSubmatchIndex(html, -1)
	for _, index := range anchors {
		rawHref := html[index[2]:index[3]]
		label := CleanHTML(html[index[4]:index[5]])
		parsed, err := url.Parse(strings.TrimSpace(rawHref))
		if err != nil || parsed.Scheme == "javascript" {
			continue
		}
		resolved := base.ResolveReference(parsed)
		if resolved.Scheme != base.Scheme || !strings.EqualFold(resolved.Hostname(), base.Hostname()) {
			continue
		}
		if resolved.User != nil {
			continue
		}
		id := ""
		if market == "spar" {
			if m := sparStorePath.FindStringSubmatch(resolved.EscapedPath()); len(m) > 1 {
				id = m[1]
			}
		} else if market == "kitgo" {
			if m := kitgoStoreID.FindStringSubmatch(resolved.EscapedPath() + "?" + resolved.RawQuery); len(m) > 1 {
				id = m[1]
			}
			// Direct linked price lists such as /cenovnici/31.xlsx are supported.
			if id == "" {
				if m := regexp.MustCompile(`(?i)/(?:cenovnici|pricelists|cenovnik)/(\d{1,5})\.(?:csv|xlsx|html?)$`).FindStringSubmatch(resolved.EscapedPath()); len(m) > 1 {
					id = m[1]
				}
			}
		}
		if id == "" {
			continue
		}
		n, err := strconv.Atoi(id)
		if err != nil || n < 1 {
			continue
		}
		id = strconv.Itoa(n)
		// The anchor often says only 'View price list'; take the nearest preceding h3.
		start := index[0] - 1100
		if start < 0 {
			start = 0
		}
		before := html[start:index[0]]
		headings := shopHeading.FindAllStringSubmatch(before, -1)
		if len(headings) > 0 {
			label = CleanHTML(headings[len(headings)-1][1])
		}
		if label == "" || strings.Contains(strings.ToLower(Normalize(label)), "cenovnik") {
			label = strings.ToUpper(market) + " store " + id
		}
		resolved.Fragment = ""
		key := market + "-" + id
		stores[key] = Source{ID: key, Market: market, Name: label, Type: market + "_store", URL: resolved.String()}
	}
	return stores
}

func ParseMarketPriceTable(body []byte, market string) (Page, error) {
	all := string(body)
	result := Page{}
	plain := CleanHTML(all)
	if market == "spar" {
		if m := sparCount.FindStringSubmatch(plain); len(m) > 1 {
			v := strings.NewReplacer(",", "", ".", "").Replace(m[1])
			result.Total, _ = strconv.Atoi(v)
		}
		result.UpdatedAt = sparUpdated.FindString(plain)
	}
	for _, table := range tableRe.FindAllStringSubmatch(all, -1) {
		columns := map[string]int{}
		var rows []ScrapedRow
		recognized := false
		for _, tr := range rowRe.FindAllStringSubmatch(table[1], -1) {
			cells := []string{}
			for _, c := range cellRe.FindAllStringSubmatch(tr[1], -1) {
				cells = append(cells, CleanHTML(c[1]))
			}
			if len(cells) < 2 {
				continue
			}
			// Header names may be reordered; never assume price is column 2.
			header := map[string]int{}
			for i, cell := range cells {
				value := Normalize(cell)
				switch {
				case value == "proizvod" || strings.Contains(value, "ime na proizvod") || strings.Contains(value, "ime na artikal") || strings.Contains(value, "naziv na stoka") || value == "product":
					header["name"] = i
				case strings.Contains(value, "prodazhna cena") || strings.Contains(value, "prodazna cena") || strings.Contains(value, "selling price"):
					header["price"] = i
				case strings.Contains(value, "redovna cena") || strings.Contains(value, "regular price"):
					header["regular"] = i
				case strings.Contains(value, "dostapnost") || strings.Contains(value, "availability"):
					header["available"] = i
				case strings.Contains(value, "kategorija") || strings.Contains(value, "category") || strings.Contains(value, "opis") || strings.Contains(value, "description"):
					header["description"] = i
				}
			}
			if _, ok := header["name"]; ok {
				if _, priceOK := header["price"]; priceOK {
					columns = header
					recognized = true
					continue
				}
			}
			if !recognized {
				continue
			}
			read := func(key string) string {
				i, ok := columns[key]
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
			if _, ok := MoneyMinor(regular); !ok {
				regular = ""
			}
			rows = append(rows, ScrapedRow{Name: name, Price: price, Regular: regular, Available: read("available"), Description: read("description")})
		}
		if len(rows) > len(result.Rows) {
			result.Rows = rows
		}
	}
	if len(result.Rows) == 0 {
		return Page{}, fmt.Errorf("%s price list has no visible priced rows (possibly JS-loaded or PDF-based)", market)
	}
	return result, nil
}

// Optional browser bridge is internal-only; it executes Javascript, clicks
// pagination, and streams each rendered HTML table as NDJSON. The Go collector
// does not trust the browser's success flag: it validates row counts and pages.
type browserChunk struct {
	HTML     string `json:"html"`
	Complete bool   `json:"complete"`
	Error    string `json:"error"`
}

func (c *Collector) browserCrawl(ctx context.Context, s Source) ([]ScrapedRow, string, error) {
	if strings.TrimSpace(c.BrowserURL) == "" {
		return nil, "", errors.New("Javascript price list requires browser bridge; set BROWSER_BRIDGE_URL")
	}
	allowed, e := c.Fetcher.allowed(ctx, s.URL)
	if e != nil {
		return nil, "", e
	}
	if !allowed {
		return nil, "", errors.New("robots.txt disallows price list")
	}
	maxPages := c.MaxPages
	if maxPages < 1 {
		maxPages = 1000
	}
	reqData, _ := json.Marshal(map[string]any{"url": s.URL, "max_pages": maxPages, "delay_ms": int(c.Fetcher.Delay.Milliseconds())})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BrowserURL, "/")+"/crawl", bytes.NewReader(reqData))
	if e != nil {
		return nil, "", e
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 4 * time.Hour}
	resp, e := client.Do(req)
	if e != nil {
		return nil, "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, "", fmt.Errorf("browser bridge HTTP %d: %s", resp.StatusCode, string(b))
	}
	dec := json.NewDecoder(bufio.NewReader(io.LimitReader(resp.Body, 256<<20)))
	seen := map[string]bool{}
	var rows []ScrapedRow
	var updated string
	total := 0
	countPages := 0
	finished := false
	for {
		var chunk browserChunk
		e := dec.Decode(&chunk)
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, "", fmt.Errorf("browser stream: %w", e)
		}
		if chunk.Error != "" {
			return nil, "", fmt.Errorf("browser: %s", chunk.Error)
		}
		if chunk.Complete {
			finished = true
			break
		}
		countPages++
		if countPages > maxPages {
			return nil, "", errors.New("price list exceeded max_pages")
		}
		p, e := ParseMarketPriceTable([]byte(chunk.HTML), s.Market)
		if e != nil {
			return nil, "", e
		}
		fp := p.Rows[0].Name + "|" + p.Rows[len(p.Rows)-1].Name
		if seen[fp] {
			return nil, "", fmt.Errorf("page %d repeats an earlier page", countPages)
		}
		seen[fp] = true
		if total == 0 {
			total = p.Total
		}
		if p.Total > 0 && total > 0 && p.Total != total {
			return nil, "", fmt.Errorf("reported product total changed: %d -> %d", total, p.Total)
		}
		if updated == "" {
			updated = p.UpdatedAt
		}
		rows = append(rows, p.Rows...)
	}
	if !finished || len(rows) == 0 {
		return nil, "", errors.New("browser stream ended without verified completion")
	}
	if total == 0 {
		return nil, "", errors.New("browser price list did not expose an authoritative product count")
	}
	if len(rows) != total {
		return nil, "", fmt.Errorf("reported %d products; retrieved %d (refusing incomplete snapshot)", total, len(rows))
	}
	return rows, updated, nil
}

func (c *Collector) collectMarketStore(ctx context.Context, s Source) ([]ScrapedRow, string, error) {
	if strings.HasSuffix(strings.ToLower(s.URL), ".csv") {
		b, e := c.Fetcher.Get(ctx, s.URL)
		if e != nil {
			return nil, "", e
		}
		r, e := ReadCSV(bytes.NewReader(b))
		return r, "", e
	}
	if strings.HasSuffix(strings.ToLower(s.URL), ".pdf") || strings.HasSuffix(strings.ToLower(s.URL), ".xlsx") {
		return nil, "", fmt.Errorf("unsupported linked price-list format for %s: %s (no values guessed)", s.ID, s.URL)
	}
	html, e := c.Fetcher.Get(ctx, s.URL)
	if e != nil {
		return nil, "", e
	}
	page, err := ParseMarketPriceTable(html, s.Market)
	if err == nil && page.Total == 0 {
		// No authoritative row count: cannot safely deactivate old products.
		// Linked full CSV exports are handled above as a distinct format.
		return nil, "", errors.New("price list has no reported total: cannot verify complete snapshot")
	}
	if err == nil && page.Total > 0 && len(page.Rows) == page.Total {
		return page.Rows, page.UpdatedAt, nil
	}
	// Dynamic grid: always require browser to finish all pages before commit.
	return c.browserCrawl(ctx, s)
}

func (c *Collector) renderedDirectory(ctx context.Context, s Source) ([]byte, error) {
	body, e := c.Fetcher.Get(ctx, s.URL)
	if e != nil {
		return nil, e
	}
	origin, e := url.Parse(s.URL)
	if e != nil {
		return nil, e
	}
	if len(MarketDirectoryLinks(body, origin, s.Market)) > 0 || c.BrowserURL == "" {
		return body, nil
	}
	// Browser render only when static HTML exposes no genuine store links.
	reqData, _ := json.Marshal(map[string]string{"url": s.URL})
	request, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BrowserURL, "/")+"/render", bytes.NewReader(reqData))
	if e != nil {
		return nil, e
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 80 * time.Second}
	res, e := client.Do(request)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("browser directory HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 12<<20))
}
func (c *Collector) syncMarketDirectory(ctx context.Context, s Source) (bool, error) {
	html, e := c.renderedDirectory(ctx, s)
	if e != nil {
		return false, e
	}
	base, e := url.Parse(s.URL)
	if e != nil {
		return false, e
	}
	links := MarketDirectoryLinks(html, base, s.Market)
	if len(links) == 0 {
		return false, fmt.Errorf("%s directory exposes no verified per-store links; refusing guessed URLs", s.Market)
	}
	ids := make([]string, 0, len(links))
	for id := range links {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	capped := s.MaxStores > 0 && len(ids) > s.MaxStores
	if capped {
		ids = ids[:s.MaxStores]
	}
	failures := []string{}
	for _, id := range ids {
		if e := ctx.Err(); e != nil {
			return false, e
		}
		if err := c.syncSource(ctx, links[id]); err != nil {
			failures = append(failures, id+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return false, fmt.Errorf("%d/%d selected %s stores failed: %s", len(failures), len(ids), s.Market, strings.Join(failures, "; "))
	}
	return !capped, nil
}
