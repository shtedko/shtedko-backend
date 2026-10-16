package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Shop adapters are for public online WooCommerce catalogs. Their prices do
// NOT imply a price or availability at any physical branch of the retailer.
// Listings without a usable price are intentionally excluded. Coverage is
// marked partial in the sync log, even if every catalog page was visited.
var (
	shopCardStart  = regexp.MustCompile(`(?is)<(?:li|div)\b[^>]*\bclass\s*=\s*["'][^"']*(?:product-grid-item|type-product|product-list-item)[^"']*["'][^>]*>`)
	shopTitleTag   = regexp.MustCompile(`(?is)<h[234]\b[^>]*>(.*?)</h[234]>`)
	shopPriceTag   = regexp.MustCompile(`(?is)<(?:span|bdi)\b[^>]*\bclass\s*=\s*["'][^"']*(?:woocommerce-Price-amount|amount)[^"']*["'][^>]*>(.*?)</(?:span|bdi)>`)
	shopPriceBlock = regexp.MustCompile(`(?is)<(?:span|div)\b[^>]*\bclass\s*=\s*["'][^"']*\bprice\b[^"']*["'][^>]*>(.*?)</(?:span|div)>`)
	shopTotalRe    = regexp.MustCompile(`(?i)\b(?:od|of|nga)\s+(\d{1,8})\s+(?:rezultati|results|rezultatet|përfundime|artikli)\b`)
	shopNextRe     = regexp.MustCompile(`(?i)\bnext\s+page-numbers\b`)
)

func ParseShopPage(body []byte) (Page, int, error) {
	html := string(body)
	starts := shopCardStart.FindAllStringIndex(html, -1)
	if len(starts) == 0 {
		return Page{}, 0, errors.New("no recognizable WooCommerce product cards")
	}
	page := Page{}
	missing := 0
	for i, start := range starts {
		finish := len(html)
		if i+1 < len(starts) {
			finish = starts[i+1][0]
		}
		chunk := html[start[0]:finish]
		headings := shopTitleTag.FindAllStringSubmatch(chunk, -1)
		name := ""
		for _, h := range headings {
			v := CleanHTML(h[1])
			if len(v) > 2 && len(v) < 500 {
				name = v
				break
			}
		}
		if name == "" {
			continue
		}
		amountTags := shopPriceTag.FindAllStringSubmatch(chunk, -1)
		price := ""
		if len(amountTags) > 0 {
			price = CleanHTML(amountTags[len(amountTags)-1][1])
		} else if blocks := shopPriceBlock.FindAllStringSubmatch(chunk, -1); len(blocks) > 0 {
			price = CleanHTML(blocks[0][1])
		}
		if price == "" {
			missing++
			continue
		}
		// A price must be explicitly labeled in MKD; generic numerals or unit prices
		// are not accepted as a retail checkout price.
		if !strings.Contains(strings.ToLower(price), "ден") && !strings.Contains(strings.ToLower(price), "mkd") {
			missing++
			continue
		}
		if _, ok := MoneyMinor(price); !ok {
			missing++
			continue
		}
		availability := "Да"
		if strings.Contains(strings.ToLower(chunk[:min(len(chunk), 400)]), "outofstock") || strings.Contains(strings.ToLower(chunk[:min(len(chunk), 400)]), "out-of-stock") {
			availability = "Не"
		}
		page.Rows = append(page.Rows, ScrapedRow{Name: name, Price: price, Available: availability})
	}
	if len(page.Rows) == 0 {
		return Page{}, missing, errors.New("shop contains no priced product cards")
	}
	full := Normalize(CleanHTML(html))
	if m := shopTotalRe.FindStringSubmatch(full); len(m) > 1 {
		page.Total, _ = strconv.Atoi(m[1])
	}
	return page, missing, nil
}

func (c *Collector) collectShop(ctx context.Context, s Source) ([]ScrapedRow, string, error) {
	first, err := url.Parse(s.URL)
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(first.Path, "/shop") {
		return nil, "", errors.New("shop source must be an explicit /shop URL")
	}
	maxPages := c.MaxPages
	if maxPages <= 0 {
		maxPages = 500
	}
	var result []ScrapedRow
	observedUnpriced := 0
	var firstTotal int
	seen := map[string]bool{}
	for pageNumber := 1; pageNumber <= maxPages; pageNumber++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		dest := *first
		if pageNumber > 1 {
			dest.Path = strings.TrimRight(first.Path, "/") + "/page/" + strconv.Itoa(pageNumber) + "/"
		}
		body, err := c.Fetcher.Get(ctx, dest.String())
		if err != nil {
			return nil, "", fmt.Errorf("shop page %d: %w", pageNumber, err)
		}
		p, unpriced, err := ParseShopPage(body)
		if err != nil {
			return nil, "", fmt.Errorf("shop page %d: %w", pageNumber, err)
		}
		if firstTotal == 0 {
			firstTotal = p.Total
		}
		fp := p.Rows[0].Name + "|" + p.Rows[len(p.Rows)-1].Name
		if seen[fp] {
			return nil, "", fmt.Errorf("repeated shop page %d", pageNumber)
		}
		seen[fp] = true
		observedUnpriced += unpriced
		result = append(result, p.Rows...)
		// If the product count is published, follow pages until the last displayed
		// index reaches total; count unpriced products as displayed cards.
		if firstTotal > 0 {
			displayed := len(result) + observedUnpriced
			if displayed >= firstTotal {
				return result, "", nil
			}
		} else {
			next := false
			for _, href := range hrefHTML.FindAllStringSubmatch(string(body), -1) {
				target, e := url.Parse(href[1])
				if e != nil {
					continue
				}
				resolved := dest.ResolveReference(target)
				nextPath := strings.TrimRight(first.Path, "/") + "/page/" + strconv.Itoa(pageNumber+1) + "/"
				if resolved.Host == first.Host && resolved.Path == nextPath {
					next = true
					break
				}
			}
			if !next {
				return result, "", nil
			}
		}
	}
	return nil, "", fmt.Errorf("shop reached max_pages=%d without proving end-of-catalog", maxPages)
}
