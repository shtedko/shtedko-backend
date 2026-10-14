package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type API struct {
	DB         *DB
	Collector  *Collector
	Sources    []Source
	AdminToken string
}

func (a *API) Routes() http.Handler {
	return requestLogger(a.routeMux())
}

// routeMux makes the actual registered method/path combinations testable against OpenAPI.
func (a *API) routeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.yaml", serveOpenAPIYAML)
	mux.HandleFunc("GET /openapi.json", serveOpenAPIJSON)
	mux.HandleFunc("GET /docs", serveSwaggerUI)
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /v1/stores", a.stores)
	mux.HandleFunc("GET /v1/markets", a.markets)
	mux.HandleFunc("GET /v1/products", a.products)
	mux.HandleFunc("GET /v1/products/{id}", a.productDetail)
	mux.HandleFunc("GET /v1/products/{id}/price-history", a.priceHistory)
	mux.HandleFunc("GET /v1/comparisons", a.compareOffers)
	mux.HandleFunc("POST /v1/basket/quote", a.basketQuote)
	mux.HandleFunc("POST /v1/baskets", a.createBasket)
	mux.HandleFunc("GET /v1/baskets/{id}", a.getBasket)
	mux.HandleFunc("DELETE /v1/baskets/{id}", a.deleteBasket)
	mux.HandleFunc("POST /v1/baskets/{id}/items", a.addBasketItem)
	mux.HandleFunc("PATCH /v1/baskets/{id}/items/{item_id}", a.patchBasketItem)
	mux.HandleFunc("DELETE /v1/baskets/{id}/items/{item_id}", a.deleteBasketItem)
	mux.HandleFunc("POST /v1/baskets/{id}/quote", a.quoteSavedBasket)
	mux.HandleFunc("GET /v1/categories", a.categories)
	mux.HandleFunc("GET /v1/comparison-groups", a.groups)
	mux.HandleFunc("GET /v1/sync-runs", a.runs)
	mux.HandleFunc("POST /v1/admin/sync", a.syncNow)
	return mux
}
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(started))
	})
}
func respond(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, code int, reason string) {
	respond(w, code, map[string]any{"error": reason})
}
func readBoundedInt(s string, def, max int) (int, error) {
	if s == "" {
		return def, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || v > max {
		return 0, fmt.Errorf("expected integer 0..%d", max)
	}
	return v, nil
}
func moneyMap(row map[string]any) {
	if v, ok := row["price_minor"].(int64); ok {
		row["price_mkd"] = float64(v) / 100
	}
	delete(row, "price_minor")
	if v, ok := row["regular_price_minor"].(int64); ok {
		row["regular_price_mkd"] = float64(v) / 100
	} else {
		row["regular_price_mkd"] = nil
	}
	delete(row, "regular_price_minor")
	if v, ok := row["available"].(int64); ok {
		row["available"] = v == 1
	}
	if v, ok := row["active"].(int64); ok {
		row["active"] = v == 1
	}
	row["currency"] = "MKD"
}
func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if _, e := a.DB.Scalar("SELECT 1"); e != nil {
		fail(w, 503, "database unavailable")
		return
	}
	respond(w, 200, map[string]string{"status": "ok"})
}
func (a *API) markets(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT s.market,COUNT(DISTINCT s.id) AS stores,COUNT(p.id) AS active_products FROM stores s LEFT JOIN products p ON p.store_id=s.id AND p.active=1 GROUP BY s.market ORDER BY s.market`)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	respond(w, 200, map[string]any{"items": rows})
}
func (a *API) stores(w http.ResponseWriter, r *http.Request) {
	rows, e := a.DB.Query(`SELECT s.id,s.market,s.name,s.source_url,s.status,s.last_success,s.last_error,
 COUNT(CASE WHEN p.active=1 THEN 1 END) AS active_products
 FROM stores s LEFT JOIN products p ON p.store_id=s.id GROUP BY s.id ORDER BY s.market,s.name`)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	respond(w, 200, map[string]any{"items": rows})
}
func (a *API) products(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, e := readBoundedInt(q.Get("limit"), 50, 200)
	if e != nil || limit == 0 {
		fail(w, 400, "limit must be 1..200")
		return
	}
	offset, e := readBoundedInt(q.Get("offset"), 0, 1000000)
	if e != nil {
		fail(w, 400, "offset must be 0..1000000")
		return
	}
	where := " WHERE 1=1"
	args := []any{}
	if q.Get("include_inactive") != "true" {
		where += " AND p.active=1"
	}
	for _, pair := range []struct{ key, col string }{{"category", "p.category"}, {"market", "s.market"}, {"store", "s.id"}} {
		if x := strings.TrimSpace(q.Get(pair.key)); x != "" {
			where += " AND " + pair.col + "=?"
			args = append(args, strings.ToLower(x))
		}
	}
	if x := strings.TrimSpace(q.Get("q")); x != "" {
		where += " AND (p.normalized_name LIKE ? OR p.name LIKE ?)"
		term := strings.ReplaceAll(strings.ReplaceAll(x, "%", ""), "_", "")
		args = append(args, "%"+Normalize(term)+"%", "%"+term+"%")
	}
	if aVal := q.Get("available"); aVal != "" {
		if aVal != "true" && aVal != "false" {
			fail(w, 400, "available must be true or false")
			return
		}
		where += " AND p.available=?"
		args = append(args, aVal == "true")
	}
	from := " FROM products p JOIN stores s ON s.id=p.store_id"
	total, e := a.DB.Scalar("SELECT COUNT(*)"+from+where, args...)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	selection := `SELECT p.id,p.name,p.description,p.category,p.comparison_key,p.size_value,p.size_unit,p.price_minor,p.regular_price_minor,p.available,p.source_url,p.source_updated_at,p.last_seen_at,p.active,s.id AS store_id,s.name AS store_name,s.market`
	rows, e := a.DB.Query(selection+from+where+" ORDER BY p.category,p.name,s.market,s.name LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	for _, row := range rows {
		moneyMap(row)
	}
	respond(w, 200, map[string]any{"items": rows, "total": total, "limit": limit, "offset": offset})
}
func (a *API) categories(w http.ResponseWriter, r *http.Request) {
	rows, e := a.DB.Query(`SELECT category,COUNT(*) AS products,COUNT(DISTINCT store_id) AS stores FROM products WHERE active=1 GROUP BY category ORDER BY products DESC`)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	respond(w, 200, map[string]any{"items": rows})
}
func (a *API) groups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	minimum, e := readBoundedInt(q.Get("min_markets"), 2, 50)
	if e != nil || minimum == 0 {
		fail(w, 400, "min_markets must be 1..50")
		return
	}
	limit, e := readBoundedInt(q.Get("limit"), 50, 200)
	if e != nil || limit == 0 {
		fail(w, 400, "limit must be 1..200")
		return
	}
	where := " WHERE p.active=1 AND p.comparison_key<>''"
	args := []any{}
	if cat := strings.TrimSpace(q.Get("category")); cat != "" {
		where += " AND p.category=?"
		args = append(args, cat)
	}
	args = append(args, minimum, limit)
	rows, e := a.DB.Query(`SELECT p.comparison_key,p.category,COUNT(*) AS products,COUNT(DISTINCT s.market) AS markets,MIN(p.price_minor) AS min_price_minor,MAX(p.price_minor) AS max_price_minor
 FROM products p JOIN stores s ON s.id=p.store_id `+where+` GROUP BY p.comparison_key,p.category HAVING COUNT(DISTINCT s.market)>=? ORDER BY markets DESC,products DESC LIMIT ?`, args...)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	for _, row := range rows {
		for _, field := range []string{"min_price_minor", "max_price_minor"} {
			if v, ok := row[field].(int64); ok {
				row[strings.Replace(field, "_minor", "_mkd", 1)] = float64(v) / 100
			}
			delete(row, field)
		}
	}
	respond(w, 200, map[string]any{"match_quality": "candidate_only_not_verified_identical_skus", "items": rows})
}
func (a *API) runs(w http.ResponseWriter, r *http.Request) {
	limit, e := readBoundedInt(r.URL.Query().Get("limit"), 30, 200)
	if e != nil || limit == 0 {
		fail(w, 400, "limit must be 1..200")
		return
	}
	rows, e := a.DB.Query(`SELECT id,store_id,status,rows_count,started_at,finished_at,error FROM sync_runs ORDER BY id DESC LIMIT ?`, limit)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	respond(w, 200, map[string]any{"items": rows})
}
func (a *API) syncNow(w http.ResponseWriter, r *http.Request) {
	if a.AdminToken == "" {
		fail(w, 503, "ADMIN_TOKEN not configured; manual sync endpoint disabled")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.AdminToken)) != 1 {
		fail(w, 401, "unauthorized")
		return
	}
	store := r.URL.Query().Get("store")
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Minute)
	defer cancel()
	var err error
	if store != "" {
		found := false
		for _, s := range a.Sources {
			if s.ID == store {
				found = true
				err = a.Collector.SyncOne(ctx, s)
				break
			}
		}
		if !found {
			fail(w, 404, "unknown configured store")
			return
		}
	} else {
		err = a.Collector.SyncAll(ctx, a.Sources)
	}
	if err != nil {
		if strings.Contains(err.Error(), "already running") {
			fail(w, 409, err.Error())
		} else {
			fail(w, 502, err.Error())
		}
		return
	}
	respond(w, 200, map[string]any{"status": "completed", "store": store})
}
