package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSPARStoreDiscoveryOnlyLinkedStorePriceLists(t *testing.T) {
	base, _ := url.Parse("https://spar.mk/pricelist")
	html := []byte(`<section><h3>SPAR M.1 Штип Центар</h3><a href="/pricelist/1">Погледни ценовник</a></section>
    <section><h3>SPAR M.2 Пехчево</h3><a href="https://spar.mk/pricelist/2">Погледни ценовник</a></section>
    <a href="/catalog">Каталог</a><a href="https://bad.example/pricelist/3">External</a><a href="/pricelist/0">Invalid</a>`)
	got := MarketDirectoryLinks(html, base, "spar")
	if len(got) != 2 || got["spar-1"].Name != "SPAR M.1 Штип Центар" || got["spar-2"].Type != "spar_store" {
		t.Fatalf("SPAR links: %#v", got)
	}
}
func TestKitgoDiscoveryLinkedPricesNotInvented(t *testing.T) {
	base, _ := url.Parse("https://superkitgo.mk/cenovnik")
	html := []byte(`<div><h3>Штип Центар</h3><a href="/download?market_id=1">Види ценовник</a></div>
    <div><h3>Пехчево</h3><a href="/cenovnici/2.csv">Види ценовник</a></div>
    <a href="javascript:show(3)">Види ценовник</a><a href="https://wrong.example/download?id=4">Види ценовник</a>`)
	got := MarketDirectoryLinks(html, base, "kitgo")
	if len(got) != 2 || got["kitgo-1"].Market != "kitgo" || !strings.HasSuffix(got["kitgo-2"].URL, "/cenovnici/2.csv") {
		t.Fatalf("kitgo links: %#v", got)
	}
}
func sparFixture(n int, names []string, count int) string {
	s := fmt.Sprintf(`<body><h2>Маркет 1 %d Производи 2026-10-16 11:13:21</h2><table><thead><tr><th>Производ</th><th>Категорија</th><th>Достапност</th><th>Цена по единица</th><th>Продажна цена</th><th>Редовна цена</th><th>Цена со попуст</th></tr></thead><tbody>`, count)
	for _, name := range names {
		s += fmt.Sprintf(`<tr><td>%s</td><td>Млечни</td><td>Да</td><td>75 ден/л</td><td>75 ден</td><td>80 ден</td><td>70 ден</td></tr>`, name)
	}
	return s + `</tbody></table></body>`
}
func TestSPARTableColumnMappingAndFailClosed(t *testing.T) {
	page, e := ParseMarketPriceTable([]byte(sparFixture(1, []string{"МЛЕКО УХТ 3.2% 1Л"}, 3)), "spar")
	if e != nil || page.Total != 3 || len(page.Rows) != 1 || page.Rows[0].Price != "75 ден" || page.Rows[0].Regular != "80 ден" {
		t.Fatalf("SPAR table: %+v %v", page, e)
	}
	if _, e := ParseMarketPriceTable([]byte(`<table><tr><th>Производ</th><th>Продажна цена</th></tr><tr><td>Се вчитуваат производите...</td></tr></table>`), "spar"); e == nil {
		t.Fatal("accepted loading-only table")
	}
}
func TestBrowserStreamChecksAllPagesAndTotals(t *testing.T) {
	for _, broken := range []string{"", "repeat", "truncated", "missing_done"} {
		t.Run(broken, func(t *testing.T) {
			store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/robots.txt" {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, `<h1>Маркет 1 2 Производи</h1><div>Се вчитуваат производите...</div>`)
			}))
			defer store.Close()
			browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/crawl" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				enc := json.NewEncoder(w)
				_ = enc.Encode(browserChunk{HTML: sparFixture(1, []string{"МЛЕКО УХТ 3.2% 1Л"}, 2)})
				if broken != "truncated" {
					second := "ЛЕБ 500 Г"
					if broken == "repeat" {
						second = "МЛЕКО УХТ 3.2% 1Л"
					}
					_ = enc.Encode(browserChunk{HTML: sparFixture(2, []string{second}, 2)})
				}
				if broken != "missing_done" {
					_ = enc.Encode(browserChunk{Complete: true})
				}
			}))
			defer browser.Close()
			c := &Collector{Fetcher: NewFetcher(0), BrowserURL: browser.URL, MaxPages: 5}
			rows, _, err := c.collectMarketStore(context.Background(), Source{ID: "spar-1", Market: "spar", Type: "spar_store", URL: store.URL + "/pricelist/1"})
			if broken == "" && (err != nil || len(rows) != 2) {
				t.Fatalf("expected two items: %v %#v", err, rows)
			}
			if broken != "" && err == nil {
				t.Fatalf("accepted %s catalog", broken)
			}
		})
	}
}
func TestSPARDirectoryStoreCapAndPreviousSnapshotPreserved(t *testing.T) {
	broken := false
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/pricelist" {
			fmt.Fprint(w, `<h3>First</h3><a href="/pricelist/1">view</a><h3>Second</h3><a href="/pricelist/2">view</a>`)
			return
		}
		if broken {
			fmt.Fprint(w, `<h2>Маркет 1 2 Производи</h2>Се вчитуваат производите...`)
			return
		}
		fmt.Fprint(w, sparFixture(1, []string{"МЛЕКО 3.2% 1Л"}, 1))
	}))
	defer store.Close()
	db := dbForTest(t)
	c := &Collector{DB: db, Fetcher: NewFetcher(0), MaxPages: 5}
	dir := Source{ID: "spar-dir", Market: "spar", Name: "SPAR directory", Type: "spar_directory", URL: store.URL + "/pricelist", MaxStores: 1}
	if err := c.SyncOne(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	items, e := db.Scalar("SELECT COUNT(*) FROM products WHERE active=1 AND store_id='spar-1'")
	if e != nil || items != 1 {
		t.Fatalf("products=%d %v", items, e)
	}
	status, _ := db.Query("SELECT status FROM sync_runs WHERE store_id='spar-dir'")
	if len(status) != 1 || status[0]["status"] != "partial" {
		t.Fatalf("directory cap should be partial: %#v", status)
	}
	broken = true
	if err := c.SyncOne(context.Background(), dir); err == nil {
		t.Fatal("broken SPAR crawl accepted")
	}
	items, _ = db.Scalar("SELECT COUNT(*) FROM products WHERE active=1 AND store_id='spar-1'")
	if items != 1 {
		t.Fatalf("snapshot lost: %d", items)
	}
}
