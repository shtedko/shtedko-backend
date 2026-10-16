package main

// Mobile application endpoints. All quotes operate on an immutable read of the
// latest local catalog. Prices are estimates, not checkout guarantees.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

type BasketItem struct {
	ID            string  `json:"id,omitempty"`
	ProductID     string  `json:"product_id,omitempty"`
	ComparisonKey string  `json:"comparison_key,omitempty"`
	Quantity      int     `json:"quantity,omitempty"` // number of retailer packs (product_id)
	Amount        float64 `json:"amount,omitempty"`   // amount of comparable goods in base units
	Unit          string  `json:"unit,omitempty"`     // ml, g, pcs (comparison_key)
}

type QuoteRequest struct {
	Items                      []BasketItem `json:"items"`
	Markets                    []string     `json:"markets,omitempty"`
	StoreIDs                   []string     `json:"store_ids,omitempty"`
	AllowCandidateMatches      bool         `json:"allow_candidate_matches"`
	IncludeUnknownAvailability bool         `json:"include_unknown_availability"`
	MaxAgeHours                *int         `json:"max_age_hours,omitempty"` // default 168; 0 opts out
	MaxStores                  int          `json:"max_stores,omitempty"`    // 0 unlimited, otherwise 1..3
}

type Offer struct {
	ProductID       string  `json:"product_id"`
	StoreID         string  `json:"store_id"`
	StoreName       string  `json:"store_name"`
	Market          string  `json:"market"`
	ProductName     string  `json:"product_name"`
	Category        string  `json:"category"`
	ComparisonKey   string  `json:"comparison_key,omitempty"`
	SizeValue       float64 `json:"size_value,omitempty"`
	SizeUnit        string  `json:"size_unit,omitempty"`
	PriceMinor      int64   `json:"price_minor"`
	Available       *bool   `json:"available"`
	SourceUpdatedAt string  `json:"source_updated_at,omitempty"`
	LastSeenAt      string  `json:"last_seen_at"`
}

type QuoteLine struct {
	ItemID                 string  `json:"item_id"`
	ProductID              string  `json:"product_id"`
	StoreID                string  `json:"store_id"`
	StoreName              string  `json:"store_name"`
	Market                 string  `json:"market"`
	ProductName            string  `json:"product_name"`
	MatchQuality           string  `json:"match_quality"`
	Packs                  int64   `json:"packs"`
	PackSize               float64 `json:"pack_size,omitempty"`
	Unit                   string  `json:"unit,omitempty"`
	RequestedAmount        float64 `json:"requested_amount,omitempty"`
	PurchasedAmount        float64 `json:"purchased_amount,omitempty"`
	UnitPriceMinor         int64   `json:"unit_price_minor"`
	UnitPriceBasis         string  `json:"unit_price_basis,omitempty"`
	NormalizedUnitPriceMKD float64 `json:"normalized_unit_price_mkd,omitempty"`
	LineTotalMinor         int64   `json:"line_total_minor"`
	LineTotalMKD           float64 `json:"line_total_mkd"`
	SourceUpdatedAt        string  `json:"source_updated_at,omitempty"`
	LastSeenAt             string  `json:"last_seen_at"`
	Availability           string  `json:"availability"`
}

type StoreQuote struct {
	StoreID           string      `json:"store_id"`
	StoreName         string      `json:"store_name"`
	Market            string      `json:"market"`
	Complete          bool        `json:"complete"`
	CoveredItems      int         `json:"covered_items"`
	RequiredItems     int         `json:"required_items"`
	MissingItems      []string    `json:"missing_items"`
	TotalMinor        *int64      `json:"total_minor"`
	TotalMKD          *float64    `json:"total_mkd"`
	PartialTotalMinor int64       `json:"partial_total_minor"`
	Lines             []QuoteLine `json:"lines"`
}

type BasketResult struct {
	Complete     bool        `json:"complete"`
	TotalMinor   *int64      `json:"total_minor"`
	TotalMKD     *float64    `json:"total_mkd"`
	Stores       int         `json:"stores"`
	MissingItems []string    `json:"missing_items"`
	Lines        []QuoteLine `json:"lines"`
}

type QuoteResponse struct {
	Currency            string       `json:"currency"`
	CalculatedAt        string       `json:"calculated_at"`
	MatchPolicy         string       `json:"match_policy"`
	FreshnessLimitHours int          `json:"freshness_limit_hours"`
	SingleStore         *StoreQuote  `json:"cheapest_single_store"`
	SplitBasket         BasketResult `json:"cheapest_split_basket"`
	StoreOptions        []StoreQuote `json:"store_options"`
	SavingsMinor        *int64       `json:"savings_minor,omitempty"`
	Warnings            []string     `json:"warnings"`
}

func amountPrice(minor int64) float64           { return float64(minor) / 100 }
func amountPointers(n int64) (*int64, *float64) { p := amountPrice(n); return &n, &p }

func readJSON(w http.ResponseWriter, r *http.Request, obj any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(obj); err != nil {
		fail(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		fail(w, 400, "request must contain one JSON object")
		return false
	}
	return true
}
func checkItem(item BasketItem) error {
	if (item.ProductID == "") == (item.ComparisonKey == "") {
		return errors.New("specify exactly one of product_id and comparison_key")
	}
	if item.ProductID != "" {
		if item.Quantity < 1 || item.Quantity > 1000 || item.Amount != 0 || item.Unit != "" {
			return errors.New("product_id requires quantity 1..1000 and no amount/unit")
		}
	} else {
		if item.Quantity != 0 || math.IsNaN(item.Amount) || math.IsInf(item.Amount, 0) || item.Amount <= 0 || item.Amount > 100000000 || !validUnit(item.Unit) {
			return errors.New("comparison_key requires amount >0 and unit g, ml or pcs (without quantity)")
		}
	}
	if len(item.ProductID) > 200 || len(item.ComparisonKey) > 300 {
		return errors.New("identifier too long")
	}
	return nil
}
func validUnit(u string) bool { return u == "g" || u == "ml" || u == "pcs" }
func validateQuote(q QuoteRequest) error {
	if len(q.Items) == 0 || len(q.Items) > 50 {
		return errors.New("items count must be 1..50")
	}
	if q.MaxStores < 0 || q.MaxStores > 3 {
		return errors.New("max_stores must be 0 (unlimited) or 1..3")
	}
	if q.MaxAgeHours != nil && (*q.MaxAgeHours < 0 || *q.MaxAgeHours > 87600) {
		return errors.New("max_age_hours must be 0..87600")
	}
	if len(q.Markets) > 30 || len(q.StoreIDs) > 100 {
		return errors.New("too many markets/store_ids")
	}
	for _, s := range append(append([]string{}, q.Markets...), q.StoreIDs...) {
		if strings.TrimSpace(s) == "" || len(s) > 120 {
			return errors.New("invalid market/store ID")
		}
	}
	for _, i := range q.Items {
		if err := checkItem(i); err != nil {
			return err
		}
		if i.ComparisonKey != "" && !q.AllowCandidateMatches {
			return errors.New("comparison_key matching is unverified; explicitly set allow_candidate_matches=true")
		}
	}
	return nil
}
func quoteMaxAge(q QuoteRequest) int {
	if q.MaxAgeHours == nil {
		return 168
	}
	return *q.MaxAgeHours
}
func onList(list []string, needle string) bool {
	if len(list) == 0 {
		return true
	}
	for _, s := range list {
		if s == needle {
			return true
		}
	}
	return false
}
func valString(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func valInt(m map[string]any, k string) int64     { v, _ := m[k].(int64); return v }
func valFloat(m map[string]any, k string) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	if v, ok := m[k].(int64); ok {
		return float64(v)
	}
	return 0
}
func valBoolPointer(m map[string]any, k string) *bool {
	if v, ok := m[k].(int64); ok {
		p := v == 1
		return &p
	}
	return nil
}
func offerFrom(m map[string]any) Offer {
	return Offer{ProductID: valString(m, "id"), StoreID: valString(m, "store_id"), StoreName: valString(m, "store_name"), Market: valString(m, "market"), ProductName: valString(m, "name"), Category: valString(m, "category"), ComparisonKey: valString(m, "comparison_key"), SizeValue: valFloat(m, "size_value"), SizeUnit: valString(m, "size_unit"), PriceMinor: valInt(m, "price_minor"), Available: valBoolPointer(m, "available"), SourceUpdatedAt: valString(m, "source_updated_at"), LastSeenAt: valString(m, "last_seen_at")}
}
func parseSourceTime(s string) (time.Time, error) {
	for _, f := range []string{time.RFC3339Nano, time.RFC3339, time.DateOnly, "02.01.2006", "02/01/2006"} {
		if dt, e := time.Parse(f, s); e == nil {
			if f == time.DateOnly || f == "02.01.2006" || f == "02/01/2006" {
				return dt.Add(24*time.Hour - time.Nanosecond), nil
			}
			return dt, nil
		}
	}
	return time.Time{}, errors.New("unrecognized date")
}
func fresh(o Offer, maxHours int, now time.Time) bool {
	if maxHours == 0 {
		return true
	}
	date := o.SourceUpdatedAt
	if date == "" {
		date = o.LastSeenAt
	}
	t, e := parseSourceTime(date)
	return e == nil && now.Sub(t) <= time.Duration(maxHours)*time.Hour
}
func eligible(o Offer, q QuoteRequest, now time.Time) bool {
	if !onList(q.Markets, o.Market) || !onList(q.StoreIDs, o.StoreID) {
		return false
	}
	if o.Available == nil && !q.IncludeUnknownAvailability {
		return false
	}
	if o.Available != nil && !*o.Available {
		return false
	}
	return fresh(o, quoteMaxAge(q), now)
}
func (a *API) loadOffersForItem(item BasketItem, q QuoteRequest, now time.Time) ([]Offer, error) {
	var rows []map[string]any
	var err error
	const fields = `SELECT p.id,p.name,p.category,p.comparison_key,p.size_value,p.size_unit,p.price_minor,p.available,p.source_updated_at,p.last_seen_at,s.id AS store_id,s.name AS store_name,s.market FROM products p JOIN stores s ON p.store_id=s.id WHERE p.active=1 AND `
	if item.ComparisonKey != "" {
		rows, err = a.DB.Query(fields+"p.comparison_key=?", item.ComparisonKey)
	} else {
		// A store-specific product can only be substituted when the caller explicitly opted into unverified comparison keys.
		base, err2 := a.DB.Query(fields+"p.id=?", item.ProductID)
		if err2 != nil {
			return nil, err2
		}
		if len(base) == 0 {
			return nil, fmt.Errorf("product_id %q was not found among active products", item.ProductID)
		}
		key := valString(base[0], "comparison_key")
		if q.AllowCandidateMatches && key != "" {
			rows, err = a.DB.Query(fields+"(p.id=? OR p.comparison_key=?)", item.ProductID, key)
		} else {
			rows = base
		}
	}
	if err != nil {
		return nil, err
	}
	offers := make([]Offer, 0, len(rows))
	for _, row := range rows {
		o := offerFrom(row)
		if eligible(o, q, now) {
			offers = append(offers, o)
		}
	}
	return offers, nil
}
func lineFor(item BasketItem, o Offer, allowCandidate bool) (QuoteLine, bool) {
	var packs int64
	mode := "exact_retailer_listing"
	if item.ComparisonKey != "" || (allowCandidate && item.ProductID != o.ProductID) {
		mode = "unverified_comparison_group"
	}
	if item.ComparisonKey != "" {
		if o.SizeValue <= 0 || o.SizeUnit != item.Unit {
			return QuoteLine{}, false
		}
		count := math.Ceil(item.Amount/o.SizeValue - 1e-10)
		if count < 1 || count > 1000000 {
			return QuoteLine{}, false
		}
		packs = int64(count)
	} else if o.ProductID == item.ProductID {
		packs = int64(item.Quantity)
	} else {
		// Substitution for product_id must match physical total quantity of the original product.
		// That original quantity is validated and converted in loadLineChoices.
		return QuoteLine{}, false
	}
	if packs == 0 || (o.PriceMinor > 0 && packs > math.MaxInt64/o.PriceMinor) {
		return QuoteLine{}, false
	}
	avail := "unknown"
	if o.Available != nil {
		if *o.Available {
			avail = "in_stock"
		} else {
			avail = "out_of_stock"
		}
	}
	basis := ""
	var normalized float64
	if o.SizeValue > 0 {
		switch o.SizeUnit {
		case "ml":
			basis = "l"
			normalized = amountPrice(o.PriceMinor) * 1000 / o.SizeValue
		case "g":
			basis = "kg"
			normalized = amountPrice(o.PriceMinor) * 1000 / o.SizeValue
		case "pcs":
			basis = "pc"
			normalized = amountPrice(o.PriceMinor) / o.SizeValue
		}
	}
	return QuoteLine{ItemID: item.ID, ProductID: o.ProductID, StoreID: o.StoreID, StoreName: o.StoreName, Market: o.Market, ProductName: o.ProductName, MatchQuality: mode, Packs: packs, PackSize: o.SizeValue, Unit: o.SizeUnit, RequestedAmount: item.Amount, PurchasedAmount: float64(packs) * o.SizeValue, UnitPriceMinor: o.PriceMinor, UnitPriceBasis: basis, NormalizedUnitPriceMKD: normalized, LineTotalMinor: packs * o.PriceMinor, LineTotalMKD: amountPrice(packs * o.PriceMinor), SourceUpdatedAt: o.SourceUpdatedAt, LastSeenAt: o.LastSeenAt, Availability: avail}, true
}
func (a *API) choices(item BasketItem, q QuoteRequest, now time.Time) ([]QuoteLine, error) {
	offers, err := a.loadOffersForItem(item, q, now)
	if err != nil {
		return nil, err
	}
	// For a product-based basket line, candidate substitutes must have the same size unit and satisfy the requested amount.
	var original *Offer
	for i := range offers {
		if offers[i].ProductID == item.ProductID {
			original = &offers[i]
			break
		}
	}
	if original == nil && item.ProductID != "" && q.AllowCandidateMatches {
		// Its source listing can be temporarily unavailable; retrieve its package size separately.
		rows, e := a.DB.Query("SELECT size_value,size_unit FROM products WHERE id=?", item.ProductID)
		if e != nil {
			return nil, e
		}
		if len(rows) > 0 {
			original = &Offer{SizeValue: valFloat(rows[0], "size_value"), SizeUnit: valString(rows[0], "size_unit")}
		}
	}
	out := make([]QuoteLine, 0, len(offers))
	for _, o := range offers {
		if item.ProductID != "" && o.ProductID != item.ProductID {
			if original == nil || original.SizeValue <= 0 || original.SizeUnit == "" || original.SizeUnit != o.SizeUnit || o.SizeValue <= 0 {
				continue
			}
			count := math.Ceil(float64(item.Quantity)*original.SizeValue/o.SizeValue - 1e-10)
			if count < 1 || count > 1000000 || (o.PriceMinor > 0 && int64(count) > math.MaxInt64/o.PriceMinor) {
				continue
			}
			line, _ := lineFor(BasketItem{ID: item.ID, ComparisonKey: o.ComparisonKey, Amount: float64(item.Quantity) * original.SizeValue, Unit: o.SizeUnit}, o, true)
			line.MatchQuality = "unverified_comparison_group"
			out = append(out, line)
			continue
		}
		l, ok := lineFor(item, o, q.AllowCandidateMatches)
		if ok {
			out = append(out, l)
		}
	}
	return out, nil
}
func bestForStore(lines []QuoteLine) map[string]QuoteLine {
	byStore := map[string]QuoteLine{}
	for _, l := range lines {
		old, ok := byStore[l.StoreID]
		if !ok || l.LineTotalMinor < old.LineTotalMinor || (l.LineTotalMinor == old.LineTotalMinor && l.ProductID < old.ProductID) {
			byStore[l.StoreID] = l
		}
	}
	return byStore
}
func quoteFromSelection(selected []QuoteLine, allItems []BasketItem) BasketResult {
	result := BasketResult{Lines: make([]QuoteLine, 0, len(selected)), MissingItems: []string{}}
	have := map[string]QuoteLine{}
	stores := map[string]bool{}
	var total int64
	for _, l := range selected {
		have[l.ItemID] = l
	}
	for _, it := range allItems {
		if l, ok := have[it.ID]; ok {
			result.Lines = append(result.Lines, l)
			stores[l.StoreID] = true
			total += l.LineTotalMinor
		} else {
			result.MissingItems = append(result.MissingItems, it.ID)
		}
	}
	result.Stores = len(stores)
	result.Complete = len(result.MissingItems) == 0
	if result.Complete {
		result.TotalMinor, result.TotalMKD = amountPointers(total)
	}
	return result
}
func chooseSplit(all []map[string]QuoteLine, items []BasketItem, maxStores int) BasketResult {
	allStoreIDs := map[string]bool{}
	for _, options := range all {
		for id := range options {
			allStoreIDs[id] = true
		}
	}
	stores := make([]string, 0, len(allStoreIDs))
	for id := range allStoreIDs {
		stores = append(stores, id)
	}
	sort.Strings(stores)
	// A no-cap split is simply the lowest per-line price. A visit limit requires subset enumeration.
	if maxStores == 0 || maxStores >= len(stores) {
		chosen := make([]QuoteLine, 0, len(items))
		for _, opts := range all {
			var best QuoteLine
			has := false
			for _, id := range stores {
				if l, ok := opts[id]; ok && (!has || l.LineTotalMinor < best.LineTotalMinor || (l.LineTotalMinor == best.LineTotalMinor && l.StoreID < best.StoreID)) {
					best = l
					has = true
				}
			}
			if has {
				chosen = append(chosen, best)
			}
		}
		return quoteFromSelection(chosen, items)
	}
	// For a practical mobile query there are normally tens of shops. Avoid factorial workloads.
	if len(stores) > 40 {
		return BasketResult{MissingItems: itemIDs(items), Lines: []QuoteLine{}}
	}
	var best BasketResult
	found := false
	var evaluate func([]string)
	evaluate = func(subset []string) {
		chosen := make([]QuoteLine, 0, len(items))
		for _, opts := range all {
			var bestForItem QuoteLine
			has := false
			for _, id := range subset {
				if l, ok := opts[id]; ok && (!has || l.LineTotalMinor < bestForItem.LineTotalMinor || (l.LineTotalMinor == bestForItem.LineTotalMinor && l.StoreID < bestForItem.StoreID)) {
					bestForItem = l
					has = true
				}
			}
			if has {
				chosen = append(chosen, bestForItem)
			}
		}
		candidate := quoteFromSelection(chosen, items)
		if !candidate.Complete {
			return
		}
		if !found || *candidate.TotalMinor < *best.TotalMinor || (*candidate.TotalMinor == *best.TotalMinor && candidate.Stores < best.Stores) {
			best = candidate
			found = true
		}
	}
	var walk func(int, []string)
	walk = func(start int, subset []string) {
		if len(subset) > 0 {
			evaluate(subset)
		}
		if len(subset) == maxStores {
			return
		}
		for i := start; i < len(stores); i++ {
			walk(i+1, append(subset, stores[i]))
		}
	}
	walk(0, nil)
	if found {
		return best
	}
	// Nothing can cover every item within the specified visit limit.
	return BasketResult{Complete: false, MissingItems: itemIDs(items), Lines: []QuoteLine{}}
}
func itemIDs(items []BasketItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func (a *API) calculate(q QuoteRequest) (QuoteResponse, error) {
	if err := validateQuote(q); err != nil {
		return QuoteResponse{}, err
	}
	now := time.Now().UTC()
	for i := range q.Items {
		if q.Items[i].ID == "" {
			q.Items[i].ID = fmt.Sprintf("item_%d", i+1)
		}
	}
	seenIDs := map[string]bool{}
	for _, it := range q.Items {
		if seenIDs[it.ID] {
			return QuoteResponse{}, errors.New("duplicate item IDs")
		}
		seenIDs[it.ID] = true
	}
	options := make([]map[string]QuoteLine, 0, len(q.Items))
	stores := map[string]*StoreQuote{}
	for _, item := range q.Items {
		choices, err := a.choices(item, q, now)
		if err != nil {
			return QuoteResponse{}, err
		}
		best := bestForStore(choices)
		options = append(options, best)
		for _, l := range best {
			if stores[l.StoreID] == nil {
				stores[l.StoreID] = &StoreQuote{StoreID: l.StoreID, StoreName: l.StoreName, Market: l.Market, RequiredItems: len(q.Items), Lines: []QuoteLine{}, MissingItems: []string{}}
			}
		}
	}
	ranked := make([]StoreQuote, 0, len(stores))
	for _, store := range stores {
		var subtotal int64
		for i, opts := range options {
			if l, ok := opts[store.StoreID]; ok {
				store.Lines = append(store.Lines, l)
				store.CoveredItems++
				subtotal += l.LineTotalMinor
			} else {
				store.MissingItems = append(store.MissingItems, q.Items[i].ID)
			}
		}
		store.PartialTotalMinor = subtotal
		store.Complete = store.CoveredItems == len(q.Items)
		if store.Complete {
			store.TotalMinor, store.TotalMKD = amountPointers(subtotal)
		}
		ranked = append(ranked, *store)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Complete != ranked[j].Complete {
			return ranked[i].Complete
		}
		if ranked[i].Complete && ranked[i].PartialTotalMinor != ranked[j].PartialTotalMinor {
			return ranked[i].PartialTotalMinor < ranked[j].PartialTotalMinor
		}
		if ranked[i].CoveredItems != ranked[j].CoveredItems {
			return ranked[i].CoveredItems > ranked[j].CoveredItems
		}
		return ranked[i].StoreID < ranked[j].StoreID
	})
	var single *StoreQuote
	for i := range ranked {
		if ranked[i].Complete {
			single = &ranked[i]
			break
		}
	}
	split := chooseSplit(options, q.Items, q.MaxStores)
	if len(stores) > 40 && q.MaxStores > 0 && q.MaxStores < len(stores) {
		return QuoteResponse{}, errors.New("max_stores constrained search supports at most 40 eligible stores; filter by markets or store_ids")
	}
	warnings := []string{"Prices are estimates, not checkout guarantees; promotions, stock and catalog updates may differ at purchase time.", "Market/source price timestamps are returned for verification; travel costs and loyalty discounts are not included."}
	if q.AllowCandidateMatches {
		warnings = append(warnings, "Comparison keys are rule-generated candidates, NOT verified same SKUs or guaranteed interchangeable products.")
	}
	if q.IncludeUnknownAvailability {
		warnings = append(warnings, "Included listings with unconfirmed stock availability.")
	}
	for _, line := range split.Lines {
		if line.SourceUpdatedAt == "" {
			warnings = append(warnings, "Some retailer feeds have no source_updated_at; freshness uses ingestion time, which does not prove the original prices were updated then.")
			break
		}
	}
	if quoteMaxAge(q) == 0 {
		warnings = append(warnings, "Price age filtering is disabled; stale prices may be included.")
	}
	response := QuoteResponse{Currency: "MKD", CalculatedAt: now.Format(time.RFC3339), MatchPolicy: "exact_retailer_listing_only", FreshnessLimitHours: quoteMaxAge(q), SingleStore: single, SplitBasket: split, StoreOptions: ranked, Warnings: warnings}
	if q.AllowCandidateMatches {
		response.MatchPolicy = "includes_unverified_comparison_groups"
	}
	if single != nil && split.Complete {
		saved := *single.TotalMinor - *split.TotalMinor
		if saved < 0 {
			saved = 0
		}
		response.SavingsMinor = &saved
	}
	return response, nil
}
