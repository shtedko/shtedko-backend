package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func authReq(t *testing.T, a *API, method, path string, body any, bearer, basket string, want int) map[string]any {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if basket != "" {
		r.Header.Set("X-Basket-Token", basket)
	}
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
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
func TestAccountsBasketIsolationAndClaim(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "auth.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	a := &API{DB: db}
	userA := authReq(t, a, "POST", "/v1/auth/register", map[string]string{"email": "UserA@example.com", "password": "long secure pass A"}, "", "", 201)
	tokenA := userA["access_token"].(string)
	if userA["user"].(map[string]any)["email"] != "usera@example.com" {
		t.Fatal("email was not normalized")
	}
	authReq(t, a, "POST", "/v1/auth/register", map[string]string{"email": "USERA@example.com", "password": "long secure pass A"}, "", "", 409)
	authReq(t, a, "POST", "/v1/auth/login", map[string]string{"email": "usera@example.com", "password": "incorrect password"}, "", "", 401)
	second := authReq(t, a, "POST", "/v1/auth/register", map[string]string{"email": "second@example.com", "password": "another strong pass"}, "", "", 201)
	tokenB := second["access_token"].(string)
	owned := authReq(t, a, "POST", "/v1/baskets", nil, tokenA, "", 201)
	id := owned["id"].(string)
	if owned["basket_token"] != nil {
		t.Fatal("account-owned basket should not expose legacy token")
	}
	authReq(t, a, "GET", "/v1/baskets/"+id, nil, tokenB, "", 404)
	authReq(t, a, "GET", "/v1/baskets/"+id, nil, "", "", 401)
	authReq(t, a, "GET", "/v1/baskets/"+id, nil, tokenA, "", 200)
	mine := authReq(t, a, "GET", "/v1/me/baskets", nil, tokenA, "", 200)
	if len(mine["items"].([]any)) != 1 {
		t.Fatal("basket missing from account list")
	}
	other := authReq(t, a, "GET", "/v1/me/baskets", nil, tokenB, "", 200)
	if len(other["items"].([]any)) != 0 {
		t.Fatal("account basket privacy leaked")
	}
	anonymous := authReq(t, a, "POST", "/v1/baskets", nil, "", "", 201)
	legacyID := anonymous["id"].(string)
	legacyToken := anonymous["basket_token"].(string)
	authReq(t, a, "POST", "/v1/baskets/"+legacyID+"/claim", nil, tokenB, legacyToken, 200)
	authReq(t, a, "GET", "/v1/baskets/"+legacyID, nil, "", legacyToken, 401)
	authReq(t, a, "GET", "/v1/baskets/"+legacyID, nil, tokenB, "", 200)
	authReq(t, a, "GET", "/v1/baskets/"+legacyID, nil, tokenA, legacyToken, 404)
	authReq(t, a, "POST", "/v1/auth/logout", nil, tokenA, "", 204)
	authReq(t, a, "GET", "/v1/auth/me", nil, tokenA, "", 401)
	login := authReq(t, a, "POST", "/v1/auth/login", map[string]string{"email": "USERA@EXAMPLE.COM", "password": "long secure pass A"}, "", "", 200)
	authReq(t, a, "GET", "/v1/auth/me", nil, login["access_token"].(string), "", 200)
}
func TestAuthMigrationAndExpiry(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.sqlite")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE baskets (id TEXT PRIMARY KEY,token_hash TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	a := &API{DB: db}
	session := authReq(t, a, "POST", "/v1/auth/register", map[string]string{"email": "expiry@example.com", "password": "strong long password"}, "", "", 201)
	token := session["access_token"].(string)
	if err := db.Exec("UPDATE user_sessions SET expires_at=? WHERE token_hash=?", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), tokenHash(token)); err != nil {
		t.Fatal(err)
	}
	authReq(t, a, "GET", "/v1/auth/me", nil, token, "", 401)
}
func TestPasswordHashAndInput(t *testing.T) {
	one, err := hashPassword("a very long password")
	if err != nil {
		t.Fatal(err)
	}
	two, err := hashPassword("a very long password")
	if err != nil {
		t.Fatal(err)
	}
	if one == two || !verifyPassword("a very long password", one) || verifyPassword("wrong", one) {
		t.Fatal("password hashing invalid")
	}
	for _, v := range []struct{ email, password string }{{"x@y", "short"}, {"not-email", "long enough text"}} {
		if _, err := validCredentials(v.email, v.password); err == nil {
			t.Fatal("accepted invalid credentials")
		}
	}
}
func TestLoginRateLimit(t *testing.T) {
	// Use an unused IP to avoid colliding with other tests.
	a := &API{DB: dbForTest(t)}
	for i := 0; i < 8; i++ {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"flood@example.com","password":"some long password"}`))
		r.RemoteAddr = "10.33.22.91:34567"
		w := httptest.NewRecorder()
		a.Routes().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("expected 401 at %d, got %d", i, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"flood@example.com","password":"some long password"}`))
	r.RemoteAddr = "10.33.22.91:34567"
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatalf("expected login throttling, got %d", w.Code)
	}
}
