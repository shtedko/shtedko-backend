package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func veroFixture(name string, price int, next string) string {
	html := `<h1>ВЕРО 01 Ценовник</h1><p>Последно ажурирање: 16/10/2026 8:02</p><table><thead><tr><th>Назив на стока</th><th>Продажна цена (со ДДВ)</th><th>Единечна цена</th><th>Достапност во продажен објект</th><th>Опис на стока</th><th>Редовна цена</th></tr></thead><tbody><tr><td>` + name + `</td><td>` + fmt.Sprint(price) + `</td><td>90 ден/парче</td><td>Да</td><td>МЛЕКО</td><td>110</td></tr></tbody></table>`
	if next != "" {
		html += `<a href="` + next + `">🠊</a>`
	}
	return html
}
func TestVeroParser(t *testing.T) {
	p, err := ParseVeroPriceTable([]byte(veroFixture("МЛЕКО 3.2% 1Л", 90, "89_2.html")))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || p.Rows[0].Available != "Да" || p.Rows[0].Price != "90" || p.Rows[0].Description != "МЛЕКО" {
		t.Fatalf("parser results: %+v", p)
	}
	if p.UpdatedAt == "" {
		t.Fatal("date missing")
	}
	if _, err := ParseVeroPriceTable([]byte("<html>No price table</html>")); err == nil {
		t.Fatal("missing price table accepted")
	}
}
func TestVeroCrawlAndDirectory(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
		case "/":
			fmt.Fprint(w, `<a href="89_1.html">ВЕРО 1</a><a href="90_1.html">ЏАМБО 2</a>`)
		case "/89_1.html":
			fmt.Fprint(w, veroFixture("МЛЕКО ПРВО 1Л", 90, "89_2.html"))
		case "/89_2.html":
			fmt.Fprint(w, veroFixture("МЛЕКО ВТОРО 1Л", 95, ""))
		case "/90_1.html":
			fmt.Fprint(w, veroFixture("МЛЕКО ТРЕТО 1Л", 90, ""))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	db := dbForTest(t)
	c := &Collector{DB: db, Fetcher: NewFetcher(0), MaxPages: 10}
	if err := c.SyncOne(context.Background(), Source{ID: "directory", Name: "Vero list", Market: "vero", Type: "vero_directory", URL: ts.URL + "/"}); err != nil {
		t.Fatal(err)
	}
	n, err := db.Scalar("SELECT COUNT(*) FROM products WHERE active=1")
	if err != nil || n != 3 {
		t.Fatalf("products=%d err=%v", n, err)
	}
	stores, err := db.Query("SELECT id,market FROM stores ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(stores) != 2 || stores[0]["market"] != "jumbo" || stores[1]["market"] != "vero" {
		t.Fatalf("stores: %+v", stores)
	}
	// Missing final page must never corrupt a previous successful snapshot.
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "robots.txt") {
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
			return
		}
		if r.URL.Path == "/89_1.html" {
			fmt.Fprint(w, veroFixture("A", 11, "89_2.html"))
			return
		}
		http.Error(w, "offline", 503)
	}))
	defer ts2.Close()
	if err := c.SyncOne(context.Background(), Source{ID: "vero-89", Name: "Vero", Market: "vero", Type: "vero_html", URL: ts2.URL + "/89_1.html"}); err == nil {
		t.Fatal("should refuse missing second page")
	}
	n, err = db.Scalar("SELECT COUNT(*) FROM products WHERE active=1 AND store_id='vero-89'")
	if err != nil || n != 2 {
		t.Fatalf("incomplete crawl erased existing products: %d %v", n, err)
	}
}
