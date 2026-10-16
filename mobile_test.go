package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func seedMobile(t *testing.T) (*DB, *API, map[string]Product) {
	t.Helper()
	db := dbForTest(t)
	stores := []StoreMeta{
		{ID: "tinex-center", Market: "tinex", Name: "Tinex Center", URL: "https://example.test/tinex"},
		{ID: "kam-north", Market: "kam", Name: "KAM North", URL: "https://example.test/kam"},
		{ID: "ramstore-vodno", Market: "ramstore", Name: "Ramstore Vodno", URL: "https://example.test/ramstore"},
		{ID: "stokomak-one", Market: "stokomak", Name: "Stokomak One", URL: "https://example.test/stokomak"},
	}
	today := time.Now().UTC().Format("2006-01-02")
	products := map[string]Product{}
	makeProduct := func(s StoreMeta, n, d, price string) Product {
		p, e := MakeProduct(s, n, d, price, "", "Da", today)
		if e != nil {
			t.Fatal(e)
		}
		products[s.ID+"/"+n] = p
		return p
	}
	samples := [][]Product{
		{makeProduct(stores[0], "МЕГЛЕ МЛЕКО 3.2% 1Л", "ТРАЈНО МЛЕКО", "79"), makeProduct(stores[0], "БЕЛ ЛЕБ 500Г", "", "45")},
		{makeProduct(stores[1], "ЗБРЕГОВ МЛЕКО 3.2% 2Л", "ТРАЈНО МЛЕКО", "145"), makeProduct(stores[1], "БЕЛ ЛЕБ 500Г", "", "65")},
		{makeProduct(stores[2], "ТРАЈНО МЛЕКО 3.2% 1Л", "", "75"), makeProduct(stores[2], "БЕЛ ЛЕБ 500Г", "", "25")},
		{makeProduct(stores[3], "БЕЛ ЛЕБ 500Г", "", "23")},
	}
	for i, s := range stores {
		if err := db.ReplaceSnapshot(s, samples[i]); err != nil {
			t.Fatal(err)
		}
	}
	return db, &API{DB: db}, products
}
func apiRequest(t *testing.T, a *API, method, path string, body any, token string, expected int) map[string]any {
	t.Helper()
	var content []byte
	if body != nil {
		var err error
		content, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(content))
	if token != "" {
		req.Header.Set("X-Basket-Token", token)
	}
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code != expected {
		t.Fatalf("%s %s expected %d got %d: %s", method, path, expected, w.Code, w.Body.String())
	}
	if w.Body.Len() == 0 {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func mobileQuote() QuoteRequest {
	return QuoteRequest{Items: []BasketItem{{ComparisonKey: "milk|unspecified|uht|3.2|plain|normal", Amount: 2000, Unit: "ml"}, {ComparisonKey: "bread|white", Amount: 500, Unit: "g"}}, AllowCandidateMatches: true}
}
func TestQuoteSingleVsSplit(t *testing.T) {
	_, api, _ := seedMobile(t)
	r := apiRequest(t, api, "POST", "/v1/basket/quote", mobileQuote(), "", 200)
	single := r["cheapest_single_store"].(map[string]any)
	split := r["cheapest_split_basket"].(map[string]any)
	// Stokomak is missing milk, so its cheap bread can't qualify for the one-store basket.
	if single["store_id"] != "ramstore-vodno" || single["total_minor"] != float64(17500) {
		t.Fatalf("wrong complete store: %v", single)
	}
	if !split["complete"].(bool) || split["total_minor"] != float64(16800) || split["stores"] != float64(2) {
		t.Fatalf("wrong split quote: %v", split)
	}
	if r["savings_minor"] != float64(700) {
		t.Fatalf("wrong savings: %v", r)
	}
	for _, v := range split["lines"].([]any) {
		line := v.(map[string]any)
		if line["match_quality"] != "unverified_comparison_group" {
			t.Fatalf("should disclose unverified matching: %v", line)
		}
	}
	q := mobileQuote()
	q.MaxStores = 1
	r = apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	split = r["cheapest_split_basket"].(map[string]any)
	if split["total_minor"] != float64(17500) || split["stores"] != float64(1) {
		t.Fatalf("max_stores=1 violated: %v", split)
	}
	q.MaxStores = 2
	r = apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	split = r["cheapest_split_basket"].(map[string]any)
	if split["total_minor"] != float64(16800) {
		t.Fatalf("max_stores=2 failed: %v", split)
	}
}
func TestQuoteFilterMissingAndOptIn(t *testing.T) {
	_, api, products := seedMobile(t)
	q := mobileQuote()
	q.AllowCandidateMatches = false
	apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 400)
	q = mobileQuote()
	q.Markets = []string{"stokomak"}
	r := apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	if r["cheapest_single_store"] != nil || r["cheapest_split_basket"].(map[string]any)["total_minor"] != nil {
		t.Fatalf("cannot report complete amount with missing milk: %v", r)
	}
	st := r["store_options"].([]any)[0].(map[string]any)
	if st["partial_total_minor"] != float64(2300) || st["total_minor"] != nil || st["covered_items"] != float64(1) {
		t.Fatalf("partial subtotal mislabelled: %v", st)
	}
	// A specific product has no cross-market replacements without permission.
	milk := products["tinex-center/МЕГЛЕ МЛЕКО 3.2% 1Л"]
	q = QuoteRequest{Items: []BasketItem{{ProductID: milk.ID, Quantity: 2}}}
	r = apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	if r["cheapest_single_store"].(map[string]any)["total_minor"] != float64(15800) {
		t.Fatalf("unexpected exact price: %v", r)
	}
	q.AllowCandidateMatches = true
	r = apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	if r["cheapest_single_store"].(map[string]any)["total_minor"] != float64(14500) {
		t.Fatalf("candidate substitution size not handled: %v", r)
	}
}
func TestCompareOffersAndPriceHistory(t *testing.T) {
	db, api, products := seedMobile(t)
	milk := products["kam-north/ЗБРЕГОВ МЛЕКО 3.2% 2Л"]
	key := url.QueryEscape(milk.ComparisonKey)
	r := apiRequest(t, api, "GET", "/v1/comparisons?comparison_key="+key+"&amount=2000&unit=ml", "", "", 200)
	if r["count"] != float64(3) {
		t.Fatalf("expected 3 offers: %v", r)
	}
	lines := r["items"].([]any)
	if lines[0].(map[string]any)["line_total_minor"] != float64(14500) || lines[0].(map[string]any)["packs"] != float64(1) {
		t.Fatalf("package comparison wrong: %v", lines)
	}
	apiRequest(t, api, "GET", "/v1/products/"+milk.ID, nil, "", 200)
	hist := apiRequest(t, api, "GET", "/v1/products/"+milk.ID+"/price-history", nil, "", 200)
	if len(hist["observations"].([]any)) != 1 {
		t.Fatalf("missing initial history: %v", hist)
	}
	milk.PriceMinor = 13800
	e := db.ReplaceSnapshot(StoreMeta{ID: "kam-north", Market: "kam", Name: "KAM North", URL: "https://example.test/kam"}, []Product{milk})
	if e != nil {
		t.Fatal(e)
	}
	hist = apiRequest(t, api, "GET", "/v1/products/"+milk.ID+"/price-history", nil, "", 200)
	if len(hist["observations"].([]any)) != 2 {
		t.Fatalf("history change missing: %v", hist)
	}
}
func TestStaleUnavailableAndUnknown(t *testing.T) {
	db, api, products := seedMobile(t)
	today := time.Now().UTC().Format(time.DateOnly)
	old := time.Now().UTC().AddDate(-1, 0, 0).Format(time.DateOnly)
	milk := products["ramstore-vodno/ТРАЈНО МЛЕКО 3.2% 1Л"]
	bread := products["ramstore-vodno/БЕЛ ЛЕБ 500Г"]
	no := false
	milk.Available = &no
	bread.SourceUpdatedAt = old
	e := db.ReplaceSnapshot(StoreMeta{ID: "ramstore-vodno", Market: "ramstore", Name: "Ramstore Vodno", URL: "https://example.test/ramstore"}, []Product{milk, bread})
	if e != nil {
		t.Fatal(e)
	}
	q := mobileQuote()
	r := apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	if r["cheapest_single_store"].(map[string]any)["store_id"] == "ramstore-vodno" {
		t.Fatalf("stale and unavailable ignored: %v", r)
	}
	// Set unknown for KAM milk; default conservative handling removes it, opt-in restores it.
	p := products["kam-north/ЗБРЕГОВ МЛЕКО 3.2% 2Л"]
	p.Available = nil
	p.SourceUpdatedAt = today
	e = db.ReplaceSnapshot(StoreMeta{ID: "kam-north", Market: "kam", Name: "KAM North", URL: "https://example.test/kam"}, []Product{p})
	if e != nil {
		t.Fatal(e)
	}
	r = apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	if r["cheapest_split_basket"].(map[string]any)["total_minor"] != float64(18100) {
		t.Fatalf("should exclude unknown stock: %v", r)
	}
	q.IncludeUnknownAvailability = true
	r = apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	if r["cheapest_split_basket"].(map[string]any)["total_minor"] != float64(16800) {
		t.Fatalf("allow unknown stock not used: %v", r)
	}
	q.MaxAgeHours = func() *int { v := 0; return &v }()
	r = apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	if len(r["warnings"].([]any)) < 3 {
		t.Fatalf("stale warning missing: %v", r)
	}
}
func TestPersistentBasketLifecycle(t *testing.T) {
	_, api, _ := seedMobile(t)
	created := apiRequest(t, api, "POST", "/v1/baskets", nil, "", 201)
	id := created["id"].(string)
	token := created["basket_token"].(string)
	endpoint := "/v1/baskets/" + id
	apiRequest(t, api, "GET", endpoint, nil, "", 401)
	apiRequest(t, api, "GET", endpoint, nil, "not-a-token", 401)
	x := apiRequest(t, api, "POST", endpoint+"/items", BasketItem{ComparisonKey: "milk|unspecified|uht|3.2|plain|normal", Amount: 1000, Unit: "ml"}, token, 201)
	itemID := x["id"].(string)
	apiRequest(t, api, "PATCH", endpoint+"/items/"+itemID, map[string]any{"amount": 2000}, token, 200)
	apiRequest(t, api, "POST", endpoint+"/items", BasketItem{ComparisonKey: "bread|white", Amount: 500, Unit: "g"}, token, 201)
	saved := apiRequest(t, api, "GET", endpoint, nil, token, 200)
	if saved["count"] != float64(2) {
		t.Fatalf("not persisted: %v", saved)
	}
	quoted := apiRequest(t, api, "POST", endpoint+"/quote", map[string]any{"allow_candidate_matches": true}, token, 200)
	if quoted["cheapest_split_basket"].(map[string]any)["total_minor"] != float64(16800) {
		t.Fatalf("saved basket quote wrong: %v", quoted)
	}
	apiRequest(t, api, "DELETE", endpoint+"/items/"+itemID, nil, token, 204)
	saved = apiRequest(t, api, "GET", endpoint, nil, token, 200)
	if saved["count"] != float64(1) {
		t.Fatalf("delete item failed: %v", saved)
	}
	apiRequest(t, api, "DELETE", endpoint, nil, token, 204)
	apiRequest(t, api, "GET", endpoint, nil, token, 404)
}
func TestValidationAndUnitSafety(t *testing.T) {
	_, api, _ := seedMobile(t)
	q := mobileQuote()
	q.Items[0].Unit = "g"
	r := apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	// Mismatched units cannot produce a fake 2 kg milk quote.
	if r["cheapest_single_store"] != nil {
		t.Fatalf("unit mismatch must not complete: %v", r)
	}
	q = mobileQuote()
	q.Items[0].Amount = -1
	apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 400)
	q = mobileQuote()
	q.MaxStores = 4
	apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 400)
	q = mobileQuote()
	q.StoreIDs = []string{"tinex-center"}
	r = apiRequest(t, api, "POST", "/v1/basket/quote", q, "", 200)
	single := r["cheapest_single_store"].(map[string]any)
	if single["total_minor"] != float64(20300) {
		t.Fatalf("store filter failed: %v", single)
	}
	// Validate basic price/history endpoint even for an unknown product.
	apiRequest(t, api, "GET", "/v1/products/does-not-exist", nil, "", 404)
}

func TestSourceDateFormats(t *testing.T) {
	for _, date := range []string{"11.10.2026", "11/10/2026", "2026-10-11", "2026-10-11T11:00:00Z"} {
		if _, err := parseSourceTime(date); err != nil {
			t.Errorf("parse %q: %v", date, err)
		}
	}
	if _, err := parseSourceTime("not a date"); err == nil {
		t.Fatal("garbage accepted")
	}
}
