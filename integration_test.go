package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dbForTest(t *testing.T) *DB {
	t.Helper()
	db, e := OpenDB(filepath.Join(t.TempDir(), "test.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Migrate(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(db.Close)
	return db
}
func TestDBAndAPI(t *testing.T) {
	db := dbForTest(t)
	a := StoreMeta{ID: "tinex-1", Market: "tinex", Name: "Tinex One", URL: "https://example.test/tinex"}
	b := StoreMeta{ID: "kam-1", Market: "kam", Name: "KAM One", URL: "https://example.test/kam"}
	p1, e := MakeProduct(a, "МЕГЛЕ МЛЕКО 3.2% 1Л", "ТРАЈНО МЛЕКО", "79", "", "Да", "2026-10-11")
	if e != nil {
		t.Fatal(e)
	}
	p2, e := MakeProduct(b, "ЗБРЕГОВ ТРАЈНО МЛЕКО 3.2% 2Л", "", "145", "", "Да", "2026-10-11")
	if e != nil {
		t.Fatal(e)
	}
	bread, e := MakeProduct(a, "БЕЛ ЛЕБ 500Г", "", "45", "", "Да", "2026-10-11")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.ReplaceSnapshot(a, []Product{p1, bread}); e != nil {
		t.Fatal(e)
	}
	if e = db.ReplaceSnapshot(b, []Product{p2}); e != nil {
		t.Fatal(e)
	}
	api := &API{DB: db}
	get := func(path string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		api.Routes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s status %d: %s", path, w.Code, w.Body.String())
		}
		var m map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &m); e != nil {
			t.Fatal(e)
		}
		return m
	}
	milk := get("/v1/products?category=milk")
	if milk["total"].(float64) != 2 {
		t.Fatalf("expected two milks got %v", milk)
	}
	tinex := get("/v1/products?category=milk&market=tinex")
	if tinex["total"].(float64) != 1 {
		t.Fatalf("expected one tinex milk: %v", tinex)
	}
	items := tinex["items"].([]any)
	if items[0].(map[string]any)["price_mkd"].(float64) != 79 {
		t.Fatalf("price wrong: %v", items)
	}
	search := get("/v1/products?q=mleko")
	if search["total"].(float64) != 2 {
		t.Fatalf("transliteration search failed: %v", search)
	}
	groups := get("/v1/comparison-groups?category=milk&min_markets=2")
	if len(groups["items"].([]any)) != 1 {
		t.Fatalf("expected one candidate group: %v", groups)
	}
	// A successful smaller refresh deactivates stale products but retains history.
	if e = db.ReplaceSnapshot(a, []Product{bread}); e != nil {
		t.Fatal(e)
	}
	if got := get("/v1/products?category=milk")["total"].(float64); got != 1 {
		t.Errorf("active milk %v", got)
	}
	if got := get("/v1/products?include_inactive=true&category=milk")["total"].(float64); got != 2 {
		t.Errorf("historical milk %v", got)
	}
	if e = db.ReplaceSnapshot(a, nil); e == nil {
		t.Error("empty feed must not deactivate")
	}
	if got := get("/v1/stores")["items"].([]any); len(got) != 2 {
		t.Errorf("stores %v", got)
	}
}
func TestCollectorTinexTwoPages(t *testing.T) {
	db := dbForTest(t)
	second := strings.ReplaceAll(strings.ReplaceAll(tableFixture, "1 - 2", "3 - 3"), `<tr><td>ЛЕБ БЕЛ 500Г</td><td>45 ден.</td><td>100г = 9 ден.</td><td>ЛЕБ</td><td>Да</td><td>45 ден.</td></tr>`, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(second))
			return
		}
		w.Write([]byte(tableFixture))
	}))
	defer server.Close()
	s := Source{ID: "tinex-test", Market: "tinex", Name: "Tinex Test", Type: "tinex", URL: server.URL + "/", Org: "512"}
	c := &Collector{DB: db, Fetcher: NewFetcher(0), MaxPages: 5}
	if e := c.SyncOne(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	count, e := db.Scalar("SELECT COUNT(*) FROM products WHERE active=1")
	if e != nil {
		t.Fatal(e)
	}
	if count != 2 {
		t.Errorf("2 unique products expected, got %d", count)
	}
}
func TestImportAndNoDataLoss(t *testing.T) {
	db := dbForTest(t)
	path := filepath.Join(t.TempDir(), "kam.csv")
	csv := "name,price_mkd,description,available\nМЕГЛЕ МЛЕКО 3.2% 1Л,79,ТРАЈНО МЛЕКО,Да\n"
	if e := os.WriteFile(path, []byte(csv), 0644); e != nil {
		t.Fatal(e)
	}
	s := Source{ID: "kam-test", Market: "kam", Name: "KAM test", Type: "csv", URL: "file://" + path}
	c := &Collector{DB: db, Fetcher: NewFetcher(0)}
	if e := c.SyncOne(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte("name,price_mkd\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := c.SyncOne(context.Background(), s); e == nil {
		t.Fatal("empty CSV should fail")
	}
	count, e := db.Scalar("SELECT COUNT(*) FROM products WHERE active=1")
	if e != nil || count != 1 {
		t.Fatalf("data lost after failed refresh: %d %v", count, e)
	}
}
func TestCron(t *testing.T) {
	cron, e := ParseCron("0 3 * * *")
	if e != nil {
		t.Fatal(e)
	}
	tz, e := time.LoadLocation("Europe/Skopje")
	if e != nil {
		t.Fatal(e)
	}
	next, e := cron.Next(time.Date(2026, 10, 11, 12, 0, 0, 0, tz), tz)
	if e != nil {
		t.Fatal(e)
	}
	local := next.In(tz)
	if local.Day() != 12 || local.Hour() != 3 || local.Minute() != 0 {
		t.Fatalf("next=%s", local)
	}
	if _, e := ParseCron("nonsense"); e == nil {
		t.Fatal("invalid cron accepted")
	}
}

func TestRobotsBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Write([]byte("User-agent: *\nDisallow: /restricted\nAllow: /public\n"))
			return
		}
		w.Write([]byte("OK"))
	}))
	defer server.Close()
	f := NewFetcher(0)
	if _, err := f.Get(context.Background(), server.URL+"/restricted/products"); err == nil {
		t.Fatal("disallowed robots path was fetched")
	}
	if _, err := f.Get(context.Background(), server.URL+"/public/products"); err != nil {
		t.Fatal(err)
	}
}
func TestHTTPAdminToken(t *testing.T) {
	db := dbForTest(t)
	api := &API{DB: db, AdminToken: "secret", Collector: &Collector{DB: db}}
	w := httptest.NewRecorder()
	api.Routes().ServeHTTP(w, httptest.NewRequest("POST", "/v1/admin/sync", nil))
	if w.Code != 401 {
		t.Fatalf("expected 401 got %d", w.Code)
	}
}
