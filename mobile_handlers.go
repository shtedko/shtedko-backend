package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *API) productDetail(w http.ResponseWriter, r *http.Request) {
	const query = `SELECT p.id,p.name,p.description,p.category,p.comparison_key,p.size_value,p.size_unit,p.price_minor,p.regular_price_minor,p.available,p.source_url,p.source_updated_at,p.last_seen_at,p.active,s.id AS store_id,s.name AS store_name,s.market FROM products p JOIN stores s ON p.store_id=s.id WHERE p.id=?`
	rows, err := a.DB.Query(query, r.PathValue("id"))
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if len(rows) == 0 {
		fail(w, 404, "product not found")
		return
	}
	moneyMap(rows[0])
	respond(w, 200, rows[0])
}
func (a *API) priceHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	limit, e := readBoundedInt(r.URL.Query().Get("limit"), 100, 500)
	if e != nil || limit == 0 {
		fail(w, 400, "limit must be 1..500")
		return
	}
	rows, e := a.DB.Query(`SELECT price_minor,observed_at,store_id FROM price_history WHERE product_id=? ORDER BY observed_at DESC LIMIT ?`, id, limit)
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	for _, m := range rows {
		m["price_mkd"] = amountPrice(valInt(m, "price_minor"))
	}
	respond(w, 200, map[string]any{"product_id": id, "currency": "MKD", "observations": rows, "note": "Observed changes on successful snapshots, not every day; not proof of past store checkout price."})
}
func stringList(v string) []string {
	if v == "" {
		return nil
	}
	list := []string{}
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			list = append(list, s)
		}
	}
	return list
}
func (a *API) compareOffers(w http.ResponseWriter, r *http.Request) {
	args := r.URL.Query()
	key := strings.TrimSpace(args.Get("comparison_key"))
	if key == "" || len(key) > 300 {
		fail(w, 400, "comparison_key is required (max 300 characters)")
		return
	}
	amount, e := strconv.ParseFloat(args.Get("amount"), 64)
	if e != nil || amount <= 0 || amount > 100000000 {
		fail(w, 400, "amount must be positive; use ml, g or pcs")
		return
	}
	unit := args.Get("unit")
	if !validUnit(unit) {
		fail(w, 400, "unit must be ml, g or pcs")
		return
	}
	maxAge := 168
	if raw := args.Get("max_age_hours"); raw != "" {
		maxAge, e = strconv.Atoi(raw)
		if e != nil || maxAge < 0 || maxAge > 87600 {
			fail(w, 400, "invalid max_age_hours")
			return
		}
	}
	unknown := false
	if raw := args.Get("include_unknown_availability"); raw != "" {
		unknown, e = strconv.ParseBool(raw)
		if e != nil {
			fail(w, 400, "invalid include_unknown_availability")
			return
		}
	}
	q := QuoteRequest{Items: []BasketItem{{ID: "comparison", ComparisonKey: key, Amount: amount, Unit: unit}}, AllowCandidateMatches: true, IncludeUnknownAvailability: unknown, MaxAgeHours: &maxAge, Markets: stringList(args.Get("markets")), StoreIDs: stringList(args.Get("store_ids"))}
	if err := validateQuote(q); err != nil {
		fail(w, 400, err.Error())
		return
	}
	choices, err := a.choices(q.Items[0], q, time.Now().UTC())
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].LineTotalMinor != choices[j].LineTotalMinor {
			return choices[i].LineTotalMinor < choices[j].LineTotalMinor
		}
		return choices[i].StoreID < choices[j].StoreID
	})
	respond(w, 200, map[string]any{"comparison_key": key, "match_quality": "unverified_comparison_group", "requested_amount": amount, "unit": unit, "currency": "MKD", "items": choices, "count": len(choices), "notice": "Amounts are purchased in whole packs; variants and brand equivalence are not verified."})
}
func (a *API) basketQuote(w http.ResponseWriter, r *http.Request) {
	var request QuoteRequest
	if !readJSON(w, r, &request) {
		return
	}
	response, e := a.calculate(request)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	respond(w, 200, response)
}
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (a *API) createBasket(w http.ResponseWriter, r *http.Request) {
	id, e := randomHex(16)
	if e != nil {
		fail(w, 500, "cannot generate basket ID")
		return
	}
	token, e := randomHex(32)
	if e != nil {
		fail(w, 500, "cannot generate basket token")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if e = a.DB.Exec(`INSERT INTO baskets(id,token_hash,created_at,updated_at) VALUES(?,?,?,?)`, id, tokenHash(token), now, now); e != nil {
		fail(w, 500, "database error")
		return
	}
	respond(w, 201, map[string]any{"id": id, "basket_token": token, "created_at": now, "note": "Store basket_token securely on the device. Provide X-Basket-Token for all basket operations; it cannot be recovered."})
}
func (a *API) authorizeBasket(w http.ResponseWriter, r *http.Request) bool {
	id := r.PathValue("id")
	token := r.Header.Get("X-Basket-Token")
	if len(token) != 64 {
		fail(w, 401, "X-Basket-Token required")
		return false
	}
	rows, e := a.DB.Query("SELECT token_hash FROM baskets WHERE id=?", id)
	if e != nil {
		fail(w, 500, "database error")
		return false
	}
	if len(rows) != 1 || subtle.ConstantTimeCompare([]byte(valString(rows[0], "token_hash")), []byte(tokenHash(token))) != 1 {
		fail(w, 404, "basket not found or token invalid")
		return false
	}
	return true
}
func (a *API) loadSavedItems(id string) ([]BasketItem, error) {
	rows, e := a.DB.Query(`SELECT id,product_id,comparison_key,quantity,amount,unit FROM basket_items WHERE basket_id=? ORDER BY created_at,id`, id)
	if e != nil {
		return nil, e
	}
	items := make([]BasketItem, 0, len(rows))
	for _, m := range rows {
		items = append(items, BasketItem{ID: valString(m, "id"), ProductID: valString(m, "product_id"), ComparisonKey: valString(m, "comparison_key"), Quantity: int(valInt(m, "quantity")), Amount: valFloat(m, "amount"), Unit: valString(m, "unit")})
	}
	return items, nil
}
func (a *API) getBasket(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeBasket(w, r) {
		return
	}
	items, e := a.loadSavedItems(r.PathValue("id"))
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	respond(w, 200, map[string]any{"id": r.PathValue("id"), "items": items, "count": len(items)})
}
func (a *API) addBasketItem(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeBasket(w, r) {
		return
	}
	var item BasketItem
	if !readJSON(w, r, &item) {
		return
	}
	if item.ID != "" {
		fail(w, 400, "item id is server-generated")
		return
	}
	if e := checkItem(item); e != nil {
		fail(w, 400, e.Error())
		return
	}
	existing, e := a.loadSavedItems(r.PathValue("id"))
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	if len(existing) >= 50 {
		fail(w, 409, "basket limit: 50 items")
		return
	}
	item.ID, e = randomHex(16)
	if e != nil {
		fail(w, 500, "cannot generate ID")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	e = a.DB.Exec(`INSERT INTO basket_items(id,basket_id,product_id,comparison_key,quantity,amount,unit,created_at) VALUES(?,?,?,?,?,?,?,?)`, item.ID, r.PathValue("id"), item.ProductID, item.ComparisonKey, item.Quantity, item.Amount, item.Unit, now)
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	_ = a.DB.Exec("UPDATE baskets SET updated_at=? WHERE id=?", now, r.PathValue("id"))
	respond(w, 201, item)
}
func (a *API) patchBasketItem(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeBasket(w, r) {
		return
	}
	id := r.PathValue("item_id")
	existing, e := a.DB.Query(`SELECT id,product_id,comparison_key,quantity,amount,unit FROM basket_items WHERE basket_id=? AND id=?`, r.PathValue("id"), id)
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	if len(existing) == 0 {
		fail(w, 404, "item not found")
		return
	}
	var change struct {
		Quantity *int     `json:"quantity,omitempty"`
		Amount   *float64 `json:"amount,omitempty"`
		Unit     *string  `json:"unit,omitempty"`
	}
	if !readJSON(w, r, &change) {
		return
	}
	row := existing[0]
	item := BasketItem{ID: id, ProductID: valString(row, "product_id"), ComparisonKey: valString(row, "comparison_key"), Quantity: int(valInt(row, "quantity")), Amount: valFloat(row, "amount"), Unit: valString(row, "unit")}
	if change.Quantity != nil {
		item.Quantity = *change.Quantity
	}
	if change.Amount != nil {
		item.Amount = *change.Amount
	}
	if change.Unit != nil {
		item.Unit = *change.Unit
	}
	if err := checkItem(item); err != nil {
		fail(w, 400, err.Error())
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if e := a.DB.Exec(`UPDATE basket_items SET quantity=?,amount=?,unit=? WHERE basket_id=? AND id=?`, item.Quantity, item.Amount, item.Unit, r.PathValue("id"), id); e != nil {
		fail(w, 500, "database error")
		return
	}
	_ = a.DB.Exec("UPDATE baskets SET updated_at=? WHERE id=?", now, r.PathValue("id"))
	respond(w, 200, item)
}
func (a *API) deleteBasketItem(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeBasket(w, r) {
		return
	}
	id := r.PathValue("id")
	itemID := r.PathValue("item_id")
	n, e := a.DB.Scalar("SELECT COUNT(*) FROM basket_items WHERE basket_id=? AND id=?", id, itemID)
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	if n == 0 {
		fail(w, 404, "item not found")
		return
	}
	if e = a.DB.Exec("DELETE FROM basket_items WHERE basket_id=? AND id=?", id, itemID); e != nil {
		fail(w, 500, "database error")
		return
	}
	_ = a.DB.Exec("UPDATE baskets SET updated_at=? WHERE id=?", time.Now().UTC().Format(time.RFC3339Nano), id)
	w.WriteHeader(204)
}
func (a *API) deleteBasket(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeBasket(w, r) {
		return
	}
	if e := a.DB.Exec("DELETE FROM baskets WHERE id=?", r.PathValue("id")); e != nil {
		fail(w, 500, "database error")
		return
	}
	w.WriteHeader(204)
}
func (a *API) quoteSavedBasket(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeBasket(w, r) {
		return
	}
	items, e := a.loadSavedItems(r.PathValue("id"))
	if e != nil {
		fail(w, 500, "database error")
		return
	}
	var options struct {
		Markets                    []string `json:"markets,omitempty"`
		StoreIDs                   []string `json:"store_ids,omitempty"`
		AllowCandidateMatches      bool     `json:"allow_candidate_matches"`
		IncludeUnknownAvailability bool     `json:"include_unknown_availability"`
		MaxAgeHours                *int     `json:"max_age_hours,omitempty"`
		MaxStores                  int      `json:"max_stores,omitempty"`
	}
	if !readJSON(w, r, &options) {
		return
	}
	q := QuoteRequest{Items: items, Markets: options.Markets, StoreIDs: options.StoreIDs, AllowCandidateMatches: options.AllowCandidateMatches, IncludeUnknownAvailability: options.IncludeUnknownAvailability, MaxAgeHours: options.MaxAgeHours, MaxStores: options.MaxStores}
	result, e := a.calculate(q)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	respond(w, 200, result)
}
