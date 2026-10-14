package main

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
static int bind_text_copy(sqlite3_stmt *st, int i, const char *value, int n) { return sqlite3_bind_text(st,i,value,n,SQLITE_TRANSIENT); }
*/
import "C"

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
	"unsafe"
)

// SQLite is embedded, durable, indexed and needs no third-party Go modules.
// All C API calls are serialized. The application can later swap this repository for PostgreSQL.
type DB struct {
	mu     sync.Mutex
	handle *C.sqlite3
}

func OpenDB(path string) (*DB, error) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var h *C.sqlite3
	rc := C.sqlite3_open_v2(cp, &h, C.SQLITE_OPEN_READWRITE|C.SQLITE_OPEN_CREATE|C.SQLITE_OPEN_FULLMUTEX, nil)
	if rc != C.SQLITE_OK {
		msg := "cannot open database"
		if h != nil {
			msg = C.GoString(C.sqlite3_errmsg(h))
			C.sqlite3_close(h)
		}
		return nil, errors.New(msg)
	}
	db := &DB{handle: h}
	if err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		db.Close()
		return nil, err
	}
	C.sqlite3_busy_timeout(h, 5000)
	return db, nil
}
func (d *DB) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle != nil {
		C.sqlite3_close(d.handle)
		d.handle = nil
	}
}
func (d *DB) errmsg() error { return errors.New(C.GoString(C.sqlite3_errmsg(d.handle))) }

func (d *DB) prepare(q string, args []any) (*C.sqlite3_stmt, error) {
	cq := C.CString(q)
	defer C.free(unsafe.Pointer(cq))
	var st *C.sqlite3_stmt
	if C.sqlite3_prepare_v2(d.handle, cq, -1, &st, nil) != C.SQLITE_OK {
		return nil, d.errmsg()
	}
	if len(args) != int(C.sqlite3_bind_parameter_count(st)) {
		C.sqlite3_finalize(st)
		return nil, fmt.Errorf("SQL arguments: got %d for %d parameters", len(args), int(C.sqlite3_bind_parameter_count(st)))
	}
	for i, arg := range args {
		n := C.int(i + 1)
		var rc C.int
		switch v := arg.(type) {
		case nil:
			rc = C.sqlite3_bind_null(st, n)
		case string:
			b := C.CString(v)
			rc = C.bind_text_copy(st, n, b, C.int(len(v)))
			C.free(unsafe.Pointer(b))
		case int:
			rc = C.sqlite3_bind_int64(st, n, C.sqlite3_int64(v))
		case int64:
			rc = C.sqlite3_bind_int64(st, n, C.sqlite3_int64(v))
		case bool:
			if v {
				rc = C.sqlite3_bind_int(st, n, 1)
			} else {
				rc = C.sqlite3_bind_int(st, n, 0)
			}
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				rc = C.sqlite3_bind_null(st, n)
			} else {
				rc = C.sqlite3_bind_double(st, n, C.double(v))
			}
		default:
			C.sqlite3_finalize(st)
			return nil, fmt.Errorf("unsupported SQL arg %T", arg)
		}
		if rc != C.SQLITE_OK {
			C.sqlite3_finalize(st)
			return nil, d.errmsg()
		}
	}
	return st, nil
}
func (d *DB) execLocked(q string, args ...any) error {
	st, err := d.prepare(q, args)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(st)
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			return nil
		}
		if rc != C.SQLITE_ROW {
			return d.errmsg()
		}
	}
}
func (d *DB) Exec(q string, args ...any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.execLocked(q, args...)
}
func (d *DB) queryLocked(q string, args ...any) ([]map[string]any, error) {
	st, err := d.prepare(q, args)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(st)
	out := make([]map[string]any, 0)
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			return out, nil
		}
		if rc != C.SQLITE_ROW {
			return nil, d.errmsg()
		}
		row := make(map[string]any)
		for i := C.int(0); i < C.sqlite3_column_count(st); i++ {
			col := C.GoString(C.sqlite3_column_name(st, i))
			switch C.sqlite3_column_type(st, i) {
			case C.SQLITE_NULL:
				row[col] = nil
			case C.SQLITE_INTEGER:
				row[col] = int64(C.sqlite3_column_int64(st, i))
			case C.SQLITE_FLOAT:
				row[col] = float64(C.sqlite3_column_double(st, i))
			case C.SQLITE_TEXT:
				row[col] = C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(st, i))))
			case C.SQLITE_BLOB:
				row[col] = C.GoBytes(unsafe.Pointer(C.sqlite3_column_blob(st, i)), C.sqlite3_column_bytes(st, i))
			}
		}
		out = append(out, row)
	}
}
func (d *DB) Query(q string, args ...any) ([]map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.queryLocked(q, args...)
}
func (d *DB) Scalar(q string, args ...any) (int64, error) {
	rows, err := d.Query(q, args...)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	for _, v := range rows[0] {
		if n, ok := v.(int64); ok {
			return n, nil
		}
	}
	return 0, errors.New("scalar not integer")
}

func (d *DB) Migrate() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS stores (id TEXT PRIMARY KEY, market TEXT NOT NULL, name TEXT NOT NULL, source_url TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'never', last_success TEXT, last_error TEXT)`,
		`CREATE INDEX IF NOT EXISTS ix_stores_market ON stores(market)`,
		`CREATE TABLE IF NOT EXISTS products (
        id TEXT PRIMARY KEY, store_id TEXT NOT NULL REFERENCES stores(id), name TEXT NOT NULL,
        description TEXT NOT NULL DEFAULT '', normalized_name TEXT NOT NULL,
        category TEXT NOT NULL DEFAULT 'other', comparison_key TEXT NOT NULL DEFAULT '',
        size_value REAL, size_unit TEXT NOT NULL DEFAULT '', price_minor INTEGER NOT NULL,
        regular_price_minor INTEGER, available INTEGER, source_url TEXT NOT NULL,
        source_updated_at TEXT, last_seen_at TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1
      )`,
		`CREATE INDEX IF NOT EXISTS ix_products_cat ON products(category,active)`,
		`CREATE INDEX IF NOT EXISTS ix_products_store ON products(store_id,active)`,
		`CREATE INDEX IF NOT EXISTS ix_products_comp ON products(comparison_key,active)`,
		`CREATE TABLE IF NOT EXISTS sync_runs (
        id INTEGER PRIMARY KEY AUTOINCREMENT, store_id TEXT NOT NULL, status TEXT NOT NULL,
        rows_count INTEGER NOT NULL DEFAULT 0, started_at TEXT NOT NULL, finished_at TEXT NOT NULL, error TEXT NOT NULL DEFAULT ''
      )`,
		`CREATE INDEX IF NOT EXISTS ix_runs_store ON sync_runs(store_id,started_at DESC)`,
		`CREATE TABLE IF NOT EXISTS baskets (id TEXT PRIMARY KEY, token_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS basket_items (id TEXT PRIMARY KEY, basket_id TEXT NOT NULL REFERENCES baskets(id) ON DELETE CASCADE,
          product_id TEXT NOT NULL DEFAULT '', comparison_key TEXT NOT NULL DEFAULT '', quantity INTEGER NOT NULL DEFAULT 0,
          amount REAL NOT NULL DEFAULT 0, unit TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS ix_basket_items_basket ON basket_items(basket_id)`,
		`CREATE TABLE IF NOT EXISTS price_history (id INTEGER PRIMARY KEY AUTOINCREMENT, product_id TEXT NOT NULL, store_id TEXT NOT NULL,
          price_minor INTEGER NOT NULL, observed_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS ix_price_history_product ON price_history(product_id, observed_at DESC)`,
	}
	for _, s := range statements {
		if err := d.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

type StoreMeta struct{ ID, Market, Name, URL string }
type Product struct {
	ID, Name, Description, Category, ComparisonKey, SourceURL string
	SizeValue                                                 float64
	SizeUnit                                                  string
	PriceMinor                                                int64
	RegularPriceMinor                                         *int64
	Available                                                 *bool
	SourceUpdatedAt                                           string
}

func (d *DB) UpsertStore(meta StoreMeta) error {
	return d.Exec(`INSERT INTO stores(id,market,name,source_url) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET market=excluded.market,name=excluded.name,source_url=excluded.source_url`, meta.ID, meta.Market, meta.Name, meta.URL)
}

// ReplaceSnapshot is atomic: stale records become inactive only after a fully
// successful, nonempty scrape. HTTP/parsing errors never wipe a previous catalog.
func (d *DB) ReplaceSnapshot(meta StoreMeta, products []Product) error {
	if len(products) == 0 {
		return errors.New("refusing empty snapshot")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.execLocked("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = d.execLocked("ROLLBACK")
		}
	}()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := d.execLocked(`INSERT INTO stores(id,market,name,source_url) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET market=excluded.market,name=excluded.name,source_url=excluded.source_url`, meta.ID, meta.Market, meta.Name, meta.URL); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range products {
		if p.Name == "" || p.ID == "" || p.PriceMinor < 0 {
			return errors.New("invalid product record")
		}
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		var size any
		if p.SizeValue > 0 {
			size = p.SizeValue
		}
		var avail any
		if p.Available != nil {
			avail = *p.Available
		}
		var regular any
		if p.RegularPriceMinor != nil {
			regular = *p.RegularPriceMinor
		}
		previous, err := d.queryLocked(`SELECT price_minor FROM products WHERE id=?`, p.ID)
		if err != nil {
			return err
		}
		if len(previous) == 0 || previous[0]["price_minor"] != p.PriceMinor {
			if err := d.execLocked(`INSERT INTO price_history(product_id,store_id,price_minor,observed_at) VALUES(?,?,?,?)`, p.ID, meta.ID, p.PriceMinor, now); err != nil {
				return err
			}
		}
		if err := d.execLocked(`INSERT INTO products(id,store_id,name,description,normalized_name,category,comparison_key,size_value,size_unit,price_minor,regular_price_minor,available,source_url,source_updated_at,last_seen_at,active)
          VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)
          ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,normalized_name=excluded.normalized_name,category=excluded.category,comparison_key=excluded.comparison_key,size_value=excluded.size_value,size_unit=excluded.size_unit,price_minor=excluded.price_minor,regular_price_minor=excluded.regular_price_minor,available=excluded.available,source_url=excluded.source_url,source_updated_at=excluded.source_updated_at,last_seen_at=excluded.last_seen_at,active=1`, p.ID, meta.ID, p.Name, p.Description, Normalize(p.Name), p.Category, p.ComparisonKey, size, p.SizeUnit, p.PriceMinor, regular, avail, p.SourceURL, nullable(p.SourceUpdatedAt), now); err != nil {
			return fmt.Errorf("upsert %q: %w", p.Name, err)
		}
	}
	// Guard against a broken scraper silently shrinking a previously large catalog.
	// Admins should investigate legitimate >40% assortment reductions before accepting them.
	prev, err := d.queryLocked(`SELECT COUNT(*) AS n FROM products WHERE store_id=? AND active=1`, meta.ID)
	if err != nil {
		return err
	}
	old := int64(0)
	if len(prev) > 0 {
		old, _ = prev[0]["n"].(int64)
	}
	if old >= 25 && int64(len(seen))*100 < old*60 {
		return fmt.Errorf("snapshot completeness guard: %d products before, %d now; refusing to deactivate previous catalog", old, len(seen))
	}
	if err := d.execLocked(`UPDATE products SET active=0 WHERE store_id=? AND last_seen_at<>?`, meta.ID, now); err != nil {
		return err
	}
	if err := d.execLocked(`UPDATE stores SET last_success=?,last_error='',status='ok' WHERE id=?`, now, meta.ID); err != nil {
		return err
	}
	if err := d.execLocked("COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (d *DB) LogRun(storeID, status string, count int, started time.Time, errText string) {
	now := time.Now().UTC().Format(time.RFC3339)
	_ = d.Exec(`INSERT INTO sync_runs(store_id,status,rows_count,started_at,finished_at,error) VALUES(?,?,?,?,?,?)`, storeID, status, count, started.UTC().Format(time.RFC3339), now, errText)
	if status != "ok" {
		_ = d.Exec(`UPDATE stores SET status=?, last_error=? WHERE id=?`, status, errText, storeID)
	}
}
