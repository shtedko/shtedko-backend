package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Source struct {
	ID     string `json:"id"`
	Market string `json:"market"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	URL    string `json:"url"`
	Org    string `json:"org,omitempty"`
}

func (s Source) Meta() StoreMeta {
	return StoreMeta{ID: s.ID, Market: s.Market, Name: s.Name, URL: s.URL}
}

type Fetcher struct {
	Client      *http.Client
	UserAgent   string
	Delay       time.Duration
	mu          sync.Mutex
	lastRequest time.Time
	robots      map[string]string
}

func NewFetcher(delay time.Duration) *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: 30 * time.Second}, UserAgent: "GroceryCatalogBot/0.1 (+respect robots.txt)", Delay: delay, robots: make(map[string]string)}
}
func (f *Fetcher) throttle(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	elapsed := time.Since(f.lastRequest)
	wait := f.Delay - elapsed
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	f.lastRequest = time.Now()
	return nil
}
func (f *Fetcher) basicGet(ctx context.Context, link string) ([]byte, int, error) {
	if err := f.throttle(ctx); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, resp.StatusCode, fmt.Errorf("GET %s: HTTP %d", link, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 18<<20))
	return body, resp.StatusCode, err
}

// Minimal conservative robots.txt handling. Fetch error => no collection.
func (f *Fetcher) allowed(ctx context.Context, link string) (bool, error) {
	u, err := url.Parse(link)
	if err != nil {
		return false, err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false, errors.New("unsupported URL scheme")
	}
	origin := u.Scheme + "://" + u.Host
	f.mu.Lock()
	text, seen := f.robots[origin]
	f.mu.Unlock()
	if !seen {
		data, status, err := f.basicGet(ctx, origin+"/robots.txt")
		if status == 404 {
			text = ""
		} else if err != nil {
			return false, fmt.Errorf("robots.txt unavailable for %s: %w", origin, err)
		} else {
			text = string(data)
		}
		f.mu.Lock()
		f.robots[origin] = text
		f.mu.Unlock()
	}
	return robotsAllows(text, u.EscapedPath()), nil
}
func robotsAllows(text, path string) bool {
	type rule struct {
		prefix string
		allow  bool
	}
	var rules []rule
	active := false
	seenRule := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		if key == "user-agent" {
			if seenRule {
				active = false
				seenRule = false
			}
			if value == "*" || strings.EqualFold(value, "GroceryCatalogBot") {
				active = true
			}
			continue
		}
		if (key == "allow" || key == "disallow") && active {
			seenRule = true
			if value != "" {
				rules = append(rules, rule{value, key == "allow"})
			}
		}
	}
	length := -1
	allowed := true
	for _, r := range rules {
		prefix := strings.TrimSuffix(strings.SplitN(r.prefix, "$", 2)[0], "*")
		if strings.HasPrefix(path, prefix) && len(prefix) >= length {
			length = len(prefix)
			allowed = r.allow
		}
	}
	return allowed
}
func (f *Fetcher) Get(ctx context.Context, link string) ([]byte, error) {
	ok, err := f.allowed(ctx, link)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("robots.txt disallows %s", link)
	}
	body, _, err := f.basicGet(ctx, link)
	return body, err
}

type Collector struct {
	DB       *DB
	Fetcher  *Fetcher
	MaxPages int
	mu       sync.Mutex
}

func (c *Collector) SyncAll(ctx context.Context, sources []Source) error {
	if !c.mu.TryLock() {
		return errors.New("sync already running")
	}
	defer c.mu.Unlock()
	var errs []string
	for _, s := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.syncSource(ctx, s); err != nil {
			log.Printf("sync %s: %v", s.ID, err)
			errs = append(errs, s.ID+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
func (c *Collector) SyncOne(ctx context.Context, s Source) error {
	if !c.mu.TryLock() {
		return errors.New("sync already running")
	}
	defer c.mu.Unlock()
	return c.syncSource(ctx, s)
}
func (c *Collector) syncSource(ctx context.Context, s Source) error {
	if s.Type == "ramstore_directory" {
		started := time.Now()
		err := c.syncDirectory(ctx, s)
		if err != nil {
			c.DB.LogRun(s.ID, "error", 0, started, err.Error())
		} else {
			c.DB.LogRun(s.ID, "ok", 0, started, "")
		}
		return err
	}
	meta := s.Meta()
	start := time.Now()
	_ = c.DB.UpsertStore(meta)
	rows, date, err := c.collect(ctx, s)
	products := make([]Product, 0, len(rows))
	if err == nil {
		for _, row := range rows {
			p, e := MakeProduct(meta, row.Name, row.Description, row.Price, row.Regular, row.Available, func() string {
				if row.UpdatedAt != "" {
					return row.UpdatedAt
				}
				return date
			}())
			if e != nil {
				err = fmt.Errorf("parse product %q: %w", row.Name, e)
				break
			}
			products = append(products, p)
		}
	}
	if err == nil {
		err = c.DB.ReplaceSnapshot(meta, products)
	}
	if err != nil {
		c.DB.LogRun(meta.ID, "error", len(products), start, err.Error())
		return err
	}
	c.DB.LogRun(meta.ID, "ok", len(products), start, "")
	log.Printf("synced %-25s %d products", s.ID, len(products))
	return nil
}
func (c *Collector) collect(ctx context.Context, s Source) ([]ScrapedRow, string, error) {
	switch s.Type {
	case "csv_url":
		body, err := c.Fetcher.Get(ctx, s.URL)
		if err != nil {
			return nil, "", err
		}
		rows, err := ReadCSV(bytes.NewReader(body))
		return rows, "", err
	case "csv":
		path := strings.TrimPrefix(s.URL, "file://")
		if path == "" {
			return nil, "", errors.New("no CSV path configured")
		}
		fp, err := os.Open(filepath.Clean(path))
		if err != nil {
			return nil, "", err
		}
		defer fp.Close()
		rows, err := ReadCSV(fp)
		return rows, "", err
	case "ramstore_html":
		body, err := c.Fetcher.Get(ctx, s.URL)
		if err != nil {
			return nil, "", err
		}
		page, err := ParsePriceTable(body)
		return page.Rows, page.UpdatedAt, err
	case "tinex":
		if s.Org == "" {
			return nil, "", errors.New("Tinex requires org store ID")
		}
		maxPages := c.MaxPages
		if maxPages <= 0 {
			maxPages = 500
		}
		var result []ScrapedRow
		var updated string
		seen := map[string]bool{}
		for pageNumber := 1; pageNumber <= maxPages; pageNumber++ {
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
			u, err := url.Parse(s.URL)
			if err != nil {
				return nil, "", err
			}
			q := u.Query()
			q.Set("org", s.Org)
			q.Set("page", strconv.Itoa(pageNumber))
			q.Set("perPage", "100")
			q.Set("search", "")
			u.RawQuery = q.Encode()
			body, err := c.Fetcher.Get(ctx, u.String())
			if err != nil {
				return nil, "", fmt.Errorf("page %d: %w", pageNumber, err)
			}
			p, err := ParsePriceTable(body)
			if err != nil {
				return nil, "", fmt.Errorf("page %d: %w", pageNumber, err)
			}
			if updated == "" {
				updated = p.UpdatedAt
			}
			fingerprint := p.Rows[0].Name + "|" + p.Rows[len(p.Rows)-1].Name
			if seen[fingerprint] {
				return nil, "", fmt.Errorf("page %d repeated earlier page; refusing partial catalog", pageNumber)
			}
			seen[fingerprint] = true
			result = append(result, p.Rows...)
			// Respect the server-reported total; it may cap perPage below 100.
			if p.Total > 0 && len(result) >= p.Total {
				return result, updated, nil
			}
			if p.Total == 0 && len(p.Rows) < 100 {
				return result, updated, nil
			}
		}
		return nil, "", fmt.Errorf("reached max_pages=%d before verifying Tinex catalog complete", maxPages)
	default:
		return nil, "", fmt.Errorf("unknown adapter %q", s.Type)
	}
}

var marketSlugRe = regexp.MustCompile(`(?i)/marketi/([a-z0-9-]+)/`)

func (c *Collector) syncDirectory(ctx context.Context, s Source) error {
	body, err := c.Fetcher.Get(ctx, s.URL)
	if err != nil {
		return err
	}
	base, err := url.Parse(s.URL)
	if err != nil {
		return err
	}
	items := map[string]Source{}
	for _, m := range marketSlugRe.FindAllStringSubmatch(string(body), -1) {
		slug := m[1]
		if slug == "" || slug == "marketi" {
			continue
		}
		dest := base.ResolveReference(&url.URL{Path: "/marketi/" + slug + "/"})
		sid := "ramstore-" + slug
		items[sid] = Source{ID: sid, Market: "ramstore", Name: "Ramstore " + strings.ReplaceAll(slug, "-", " "), Type: "ramstore_html", URL: dest.String()}
	}
	if len(items) == 0 {
		return fmt.Errorf("directory %s exposes no links to store price pages; add explicit ramstore_html sources", s.URL)
	}
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var errorsSeen []string
	for _, id := range ids {
		if err := c.syncSource(ctx, items[id]); err != nil {
			errorsSeen = append(errorsSeen, id+": "+err.Error())
		}
	}
	if len(errorsSeen) > 0 {
		return fmt.Errorf("%d/%d stores failed: %s", len(errorsSeen), len(ids), strings.Join(errorsSeen, "; "))
	}
	return nil
}
