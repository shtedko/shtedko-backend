package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func portalHTML(start, total int, items []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<html><body>Прикажи %d - %d од %d артикли Датум и време на последно ажурирање на цените: 16/10/2026 18:19`, start, start+len(items)-1, total)
	b.WriteString(`<table><tr><th>Назив на стока-производ</th><th>Продажна цена</th><th>Единечна цена</th><th>Опис на стока</th><th>Достапност во продажен објект</th><th>Редовна цена</th><th>Промотивна или попустна цена</th></tr>`)
	for _, name := range items {
		fmt.Fprintf(&b, `<tr><td>%s</td><td>52 ден.</td><td>1л = 52.00 ден.</td><td>Млеко</td><td>Да</td><td>57 ден.</td><td>52 ден.</td></tr>`, name)
	}
	b.WriteString(`</table></body></html>`)
	return b.String()
}
func TestPricePortalCollectPagesAndValidateTotal(t *testing.T) {
	requests := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		requests = append(requests, r.URL.RawQuery)
		if r.URL.Query().Get("org") != "5" {
			http.Error(w, "unknown", 400)
			return
		}
		switch r.URL.Query().Get("page") {
		case "1":
			fmt.Fprint(w, portalHTML(1, 2, []string{"МЛЕКО КРАВЈО 1Л"}))
		case "2":
			fmt.Fprint(w, portalHTML(2, 2, []string{"ЈОГУРТ 1Л"}))
		default:
			http.Error(w, "missing", 404)
		}
	}))
	defer srv.Close()
	c := &Collector{Fetcher: NewFetcher(0), MaxPages: 3}
	rows, updated, err := c.collectPricePortal(context.Background(), Source{Type: "price_portal", URL: srv.URL, Org: "5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || updated != "16/10/2026" {
		t.Fatalf("rows=%d date=%s", len(rows), updated)
	}
	if len(requests) != 2 {
		t.Fatalf("got %d page calls, want 2", len(requests))
	}
	for _, q := range requests {
		v, _ := url.ParseQuery(q)
		if v.Get("perPage") != "100" {
			t.Fatal(q)
		}
	}
}
func TestPricePortalRejectRepeatsAndChangingTotals(t *testing.T) {
	for _, broken := range []string{"repeat", "changing", "missing_total"} {
		t.Run(broken, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/robots.txt" {
					http.NotFound(w, r)
					return
				}
				page := r.URL.Query().Get("page")
				if page == "1" {
					fmt.Fprint(w, portalHTML(1, 3, []string{"МИНЕРАЛНА ВОДА 1Л"}))
					return
				}
				switch broken {
				case "repeat":
					fmt.Fprint(w, portalHTML(2, 3, []string{"МИНЕРАЛНА ВОДА 1Л"}))
				case "changing":
					fmt.Fprint(w, portalHTML(2, 7, []string{"ЈОГУРТ 1Л"}))
				case "missing_total":
					fmt.Fprint(w, strings.ReplaceAll(portalHTML(2, 3, []string{"ЈОГУРТ 1Л"}), "од 3 артикли", "недостапно"))
				}
			}))
			defer srv.Close()
			c := &Collector{Fetcher: NewFetcher(0), MaxPages: 5}
			if _, _, err := c.collectPricePortal(context.Background(), Source{Org: "13", URL: srv.URL}); err == nil {
				t.Fatal("accepted corrupt catalog")
			}
		})
	}
}
func TestPortalDiscoveryAndFallback(t *testing.T) {
	markup := []byte(`<select id="perPage"><option value="20">20</option></select><select id="org" name="org"><option value="">Изберете маркет</option><option value="5">Сто Комак Скопје</option><option value="17">Сто Комак Битола</option></select>`)
	options := PortalStoreOptions(markup)
	if len(options) != 2 || options["5"] != "Сто Комак Скопје" || options["17"] != "Сто Комак Битола" {
		t.Fatalf("options %#v", options)
	}
	if len(PortalStoreOptions([]byte(`<select name="perPage"><option value="100">100</option></select>`))) != 0 {
		t.Fatal("treated perPage as stores")
	}
}
func TestPortalDirectoryActualSync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("org") == "" {
			fmt.Fprint(w, `<select id="org"><option value="5">Скопје</option><option value="9">Велес</option></select>`)
			return
		}
		fmt.Fprint(w, portalHTML(1, 1, []string{"МЛЕКО ТРАЈНО 1Л"}))
	}))
	defer srv.Close()
	db, err := OpenDB(t.TempDir() + "/test.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	c := &Collector{DB: db, Fetcher: NewFetcher(0), MaxPages: 3}
	s := Source{ID: "zito-directory", Market: "zito", Name: "Zito stores", Type: "price_portal_directory", URL: srv.URL, Org: "13"}
	complete, err := c.syncPricePortalDirectory(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("should have discovered stores")
	}
	// When the directory lists stores, it takes precedence over the fallback ID.
	count, err := db.Scalar("SELECT COUNT(*) FROM stores")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("found %d stores", count)
	}
}
func TestKAMOfferParsers(t *testing.T) {
	cases := []struct {
		name, html string
		want       int
	}{
		{"jsonld", `<script type="application/ld+json">{"@graph":[{"@type":"Product","name":"МЛЕКО 1 Л","offers":{"price":"69","priceCurrency":"MKD"}},{"@type":"Product","name":"No price","offers":{"price":"12","priceCurrency":"EUR"}}]}</script>`, 1},
		{"html", `<div class="product-item"><h3>КАШКАВАЛ ГАУДА</h3><div>1 кг <span>369 ДЕН.</span> <span>319 ДЕН.</span> 1 кг = 319.0</div></div>`, 1},
		{"no-prices", `<h3>КАШКАВАЛ ГАУДА</h3><p>1 кг -20%</p>`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, e := ParseKAMOffers([]byte(tc.html))
			if len(r) != tc.want {
				t.Fatalf("rows=%#v err=%v", r, e)
			}
			if tc.want == 0 && e == nil {
				t.Fatal("must fail closed")
			}
		})
	}
}
func TestPortalSchedulerConfigHasLiveMarketSources(t *testing.T) {
	c, err := ReadConfig("sources.json")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, s := range c.Sources {
		seen[s.Market] = s.Type
	}
	for _, market := range []string{"stokomak", "zito"} {
		if seen[market] != "price_portal_directory" {
			t.Errorf("%s is %s", market, seen[market])
		}
	}
	if seen["kam"] != "kam_offers" {
		t.Errorf("KAM still CSV: %s", seen["kam"])
	}
	// Scheduler is still active and not replaced by a manual import run.
	if _, err := ParseCron(c.Cron); err != nil {
		t.Fatal(err)
	}
}

func TestPortalFallbackAndFailedRefreshKeepsInventory(t *testing.T) {
	failPages := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("org") == "" {
			fmt.Fprint(w, `<div>Dropdown populated by JavaScript, unavailable to HTML parser</div>`)
			return
		}
		if failPages {
			fmt.Fprint(w, `<html>broken website without price table</html>`)
			return
		}
		fmt.Fprint(w, portalHTML(1, 1, []string{"МЛЕКО 3.2% 1Л"}))
	}))
	defer srv.Close()
	db, err := OpenDB(t.TempDir() + "/test.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	c := &Collector{DB: db, Fetcher: NewFetcher(0), MaxPages: 3}
	source := Source{ID: "stokomak-directory", Market: "stokomak", Type: "price_portal_directory", URL: srv.URL, Org: "5"}
	if e := c.SyncOne(context.Background(), source); e != nil {
		t.Fatal(e)
	}
	n, err := db.Scalar("SELECT COUNT(*) FROM products WHERE active=1 AND store_id='stokomak-5'")
	if err != nil || n != 1 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	statuses, err := db.Query("SELECT status FROM sync_runs WHERE store_id='stokomak-directory' ORDER BY id DESC LIMIT 1")
	if err != nil || len(statuses) != 1 || statuses[0]["status"] != "partial" {
		t.Fatalf("directory should say partial: %#v %v", statuses, err)
	}
	failPages = true
	if e := c.SyncOne(context.Background(), source); e == nil {
		t.Fatal("broken refresh should fail")
	}
	n, err = db.Scalar("SELECT COUNT(*) FROM products WHERE active=1 AND store_id='stokomak-5'")
	if err != nil || n != 1 {
		t.Fatalf("lost previous data after failed crawl: %d %v", n, err)
	}
}
