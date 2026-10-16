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

func kipperFixture(name string) string {
	return `<html><body><h1>Киппер 041, Куманово</h1><p>Датум и време на последно ажурирање на цените: 16 October 2026 - 07:59</p>
<table><thead><tr><th>Име на артикал</th><th>Продажна цена</th><th>Единечна цена</th><th>Опис на стока</th><th>Достапност во маркет</th><th>Редовна цена</th><th>Акциска цена</th><th>Вид на продажно поттикнување</th></tr></thead>
<tbody><tr><td>` + name + `</td><td>62,50 ден</td><td>62,50 ден/л</td><td>УХТ</td><td>Да</td><td>70,00 ден</td><td>62,50 ден</td><td>Попуст</td></tr></tbody></table></body></html>`
}

func TestKipperTableParsesPhysicalPrices(t *testing.T) {
	p, err := ParseKipperStoreTable([]byte(kipperFixture("ТРАЈНО МЛЕКО 3.2% 1Л")))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || p.Rows[0].Price != "62,50 ден" || p.Rows[0].Regular != "70,00 ден" || p.Rows[0].Available != "Да" {
		t.Fatalf("unexpected rows %+v", p)
	}
	if !strings.Contains(p.UpdatedAt, "16 october 2026") {
		t.Fatalf("updated at %q", p.UpdatedAt)
	}
}
func TestKipperTableFailsClosed(t *testing.T) {
	for _, markup := range []string{
		`<table><tr><th>Име на артикал</th><th>Продажна цена</th></tr></table>`,
		`<table><tr><th>Име на артикал</th><th>Продажна цена</th></tr><tr><td>Млеко</td><td>-</td></tr></table>`,
		`<div>Products are loaded from JavaScript</div>`,
	} {
		if _, err := ParseKipperStoreTable([]byte(markup)); err == nil {
			t.Fatalf("accepted unpriced page: %s", markup)
		}
	}
}
func TestKipperDiscoverStoreLinks(t *testing.T) {
	base, _ := url.Parse("https://kipper.mk/mk/category/market/")
	html := `<h3><a href="https://kipper.mk/mk/kipper-41/">Киппер 041, Куманово</a></h3>
 <h3><a href="/mk/%D0%BA%D0%B8%D0%BF%D0%BF%D0%B5%D1%80-125-%D1%82%D0%B5%D1%82%D0%BE%D0%B2%D0%BE/">Киппер 125, Тетово</a></h3>
 <a href="https://outside.example/mk/kipper-4/">Киппер 004 фалсификат</a>
 <a href="/mk/shop/">Продукти</a>`
	sources := KipperStoreLinks([]byte(html), base)
	if len(sources) != 2 || sources["kipper-041"].Market != "kipper" || sources["kipper-125"].Type != "kipper_store" {
		t.Fatalf("store links: %#v", sources)
	}
}
func TestKipperDirectoryPagesAndSync(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
		case "/mk/category/market/":
			fmt.Fprint(w, `<h3><a href="/mk/kipper-41/">Киппер 041, Куманово</a></h3><a href="/mk/category/market/page/2/">2</a>`)
		case "/mk/category/market/page/2/":
			fmt.Fprint(w, `<h3><a href="/mk/kipper-80-aerodrom/">Kipper 080, Skopje - Aerodrom</a></h3>`)
		case "/mk/kipper-41/":
			fmt.Fprint(w, kipperFixture("МЛЕКО ТРАЈНО 3.2% 1Л"))
		case "/mk/kipper-80-aerodrom/":
			fmt.Fprint(w, kipperFixture("ЛЕБ БЕЛ 500Г"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	db := dbForTest(t)
	c := &Collector{DB: db, Fetcher: NewFetcher(0), MaxPages: 5}
	dir := Source{ID: "kipper-dir", Market: "kipper", Name: "Branch directory", Type: "kipper_directory", URL: server.URL + "/mk/category/market/"}
	if err := c.SyncOne(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	count, err := db.Scalar("SELECT COUNT(*) FROM products WHERE active=1 AND store_id IN ('kipper-041','kipper-080')")
	if err != nil || count != 2 {
		t.Fatalf("products %d %v", count, err)
	}
	status, err := db.Query("SELECT status FROM sync_runs WHERE store_id='kipper-dir' ORDER BY id DESC LIMIT 1")
	if err != nil || len(status) != 1 || status[0]["status"] != "partial" {
		t.Fatalf("directory cannot prove every Kipper branch: %#v %v", status, err)
	}
}
func TestKipperFailedRefreshPreservesSnapshot(t *testing.T) {
	broken := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if broken {
			fmt.Fprint(w, `<table><tr><th>Име на артикал</th><th>Продажна цена</th></tr></table>`)
			return
		}
		fmt.Fprint(w, kipperFixture("ПИЛЕШКИ СТЕК 1КГ"))
	}))
	defer server.Close()
	db := dbForTest(t)
	c := &Collector{DB: db, Fetcher: NewFetcher(0)}
	source := Source{ID: "kipper-041", Market: "kipper", Name: "Kipper 041", Type: "kipper_store", URL: server.URL + "/mk/kipper-41/"}
	if err := c.SyncOne(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	broken = true
	if err := c.SyncOne(context.Background(), source); err == nil {
		t.Fatal("blank price table should fail")
	}
	count, e := db.Scalar("SELECT COUNT(*) FROM products WHERE active=1 AND store_id='kipper-041'")
	if e != nil || count != 1 {
		t.Fatalf("lost last good price data: %d %v", count, e)
	}
}
func TestKipperEnabledInLiveConfig(t *testing.T) {
	cfg, err := ReadConfig("sources.json")
	if err != nil {
		t.Fatal(err)
	}
	foundShop, foundBranches := false, false
	for _, s := range cfg.Sources {
		if s.Market == "kipper" && s.Type == "kipper_directory" {
			foundBranches = true
		}
		if s.Market == "kipper-online" && s.Type == "woo_shop" {
			foundShop = true
		}
	}
	if !foundShop || !foundBranches {
		t.Fatalf("Kipper feed missing: shop=%v branches=%v", foundShop, foundBranches)
	}
}
