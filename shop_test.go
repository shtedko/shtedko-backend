package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func shopFixture(title string, price string, page, total int) string {
	html := fmt.Sprintf(`<p>Прикажувам %d–%d од %d резултати</p><ul class="products"><li class="product type-product"><h3 class="woocommerce-loop-product__title">%s</h3>`, (page-1)*1+1, page, total, title)
	if price != "" {
		html += `<span class="price"><span class="woocommerce-Price-amount amount"><bdi>` + price + ` ден</bdi></span></span>`
	}
	html += `</li></ul>`
	return html
}
func TestShopPagePrices(t *testing.T) {
	page, unpriced, err := ParseShopPage([]byte(shopFixture("ЛЕБ 500Г", "42", 1, 2)))
	if err != nil || len(page.Rows) != 1 || page.Total != 2 || unpriced != 0 {
		t.Fatalf("page: %+v unpriced=%d err=%v", page, unpriced, err)
	}
	_, _, err = ParseShopPage([]byte(shopFixture("ЛЕБ 500Г", "", 1, 2)))
	if err == nil {
		t.Fatal("missing price accepted")
	}
}
func TestShopCrawlPartialPrices(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
		case "/shop/":
			fmt.Fprint(w, shopFixture("МЛЕКО 3.2% 1Л", "80", 1, 2))
		case "/shop/page/2/":
			fmt.Fprint(w, shopFixture("ЛЕБ 500Г", "50", 2, 2))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	db := dbForTest(t)
	c := &Collector{DB: db, Fetcher: NewFetcher(0), MaxPages: 3}
	s := Source{ID: "bigshop-test", Name: "Online only", Market: "bigshop-online", Type: "woo_shop", URL: ts.URL + "/shop/"}
	if err := c.SyncOne(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	total, e := db.Scalar("SELECT COUNT(*) FROM products WHERE store_id='bigshop-test'")
	if e != nil || total != 2 {
		t.Fatalf("product count %d: %v", total, e)
	}
	status, e := db.Query("SELECT status FROM stores WHERE id='bigshop-test'")
	if e != nil || status[0]["status"] != "partial" {
		t.Fatalf("must disclose partial-online status: %v %v", status, e)
	}
}
func TestShopRejectRepeatingPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "robots.txt") {
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
			return
		}
		fmt.Fprint(w, shopFixture("МЛЕКО 1Л", "80", 1, 400))
	}))
	defer ts.Close()
	db := dbForTest(t)
	c := &Collector{DB: db, Fetcher: NewFetcher(0), MaxPages: 4}
	_, _, err := c.collectShop(context.Background(), Source{URL: ts.URL + "/shop/"})
	if err == nil {
		t.Fatal("repeated page returned successful incomplete catalog")
	}
}
