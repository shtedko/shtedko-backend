package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
)

// Opaque, randomly generated bearer sessions; only SHA-256 hashes are stored.
// Passwords use salted PBKDF2-HMAC-SHA256 (210,000 iterations) to keep the
// repository dependency-free. Deployments requiring Argon2id should replace
// this KDF with a reviewed external library before public launch.
const passwordIterations = 210000
const sessionLifetime = 7 * 24 * time.Hour
const sessionTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

type authLimiter struct {
	mu      sync.Mutex
	entries map[string][]time.Time
}

func (l *authLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string][]time.Time{}
	}
	now := time.Now()
	if len(l.entries) > 5000 {
		for k, times := range l.entries {
			if len(times) == 0 || now.Sub(times[len(times)-1]) > 10*time.Minute {
				delete(l.entries, k)
			}
		}
		if len(l.entries) > 5000 {
			return false
		}
	}
	times := l.entries[key][:0]
	for _, t := range l.entries[key] {
		if now.Sub(t) < 10*time.Minute {
			times = append(times, t)
		}
	}
	if len(times) >= 8 {
		l.entries[key] = times
		return false
	}
	l.entries[key] = append(times, now)
	return true
}

var loginLimit authLimiter
var signupLimit authLimiter

func pbkdf2SHA256(password string, salt []byte, iterations int) []byte {
	mac := hmac.New(sha256.New, []byte(password))
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	key := append([]byte(nil), u...)
	for i := 1; i < iterations; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(nil)
		for j := range key {
			key[j] ^= u[j]
		}
	}
	return key
}
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2SHA256(password, salt, passwordIterations)
	return fmt.Sprintf("pbkdf2-sha256$%d$%x$%x", passwordIterations, salt, key), nil
}
func verifyPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || parts[1] != "210000" {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil || len(salt) != 16 {
		return false
	}
	expected, err := hex.DecodeString(parts[3])
	if err != nil || len(expected) != 32 {
		return false
	}
	got := pbkdf2SHA256(password, salt, passwordIterations)
	return subtle.ConstantTimeCompare(got, expected) == 1
}
func validCredentials(email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || len(email) > 254 || !strings.Contains(email, ".") {
		return "", errors.New("valid email required")
	}
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must have 12–1024 UTF-8 bytes")
	}
	return email, nil
}
func (a *API) createSession(userID string) (map[string]any, error) {
	token, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	exp := now.Add(sessionLifetime)
	if err := a.DB.Exec(`INSERT INTO user_sessions(token_hash,user_id,created_at,expires_at) VALUES(?,?,?,?)`, tokenHash(token), userID, now.Format(sessionTimeLayout), exp.Format(sessionTimeLayout)); err != nil {
		return nil, err
	}
	return map[string]any{"access_token": token, "token_type": "Bearer", "expires_at": exp.Format(sessionTimeLayout), "expires_in": int(sessionLifetime.Seconds())}, nil
}
func (a *API) bearerUser(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", errors.New("bearer token required")
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if len(token) != 64 {
		return "", errors.New("invalid bearer token")
	}
	// Strict ASCII hex token avoids ambiguous inputs.
	if _, err := hex.DecodeString(token); err != nil {
		return "", errors.New("invalid bearer token")
	}
	rows, err := a.DB.Query(`SELECT s.user_id FROM user_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?`, tokenHash(token), time.Now().UTC().Format(sessionTimeLayout))
	if err != nil {
		return "", err
	}
	if len(rows) != 1 {
		return "", errors.New("expired or invalid bearer token")
	}
	return valString(rows[0], "user_id"), nil
}
func (a *API) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := a.bearerUser(r)
	if err != nil {
		fail(w, 401, "valid bearer token required")
		return "", false
	}
	return id, true
}
func (a *API) register(w http.ResponseWriter, r *http.Request) {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		host = r.RemoteAddr
	}
	if !signupLimit.allow("signup:" + host) {
		fail(w, 429, "too many registrations; retry later")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	email, err := validCredentials(input.Email, input.Password)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	pw, err := hashPassword(input.Password)
	if err != nil {
		fail(w, 500, "could not create account")
		return
	}
	id, err := randomHex(16)
	if err != nil {
		fail(w, 500, "could not create account")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Atomic uniqueness enforced by SQLite's primary key, including across replicas.
	if err = a.DB.Exec(`INSERT INTO users(id,email,password_hash,created_at) VALUES(?,?,?,?)`, id, email, pw, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			fail(w, 409, "email already registered")
		} else {
			fail(w, 500, "could not create account")
		}
		return
	}
	session, err := a.createSession(id)
	if err != nil {
		fail(w, 500, "could not create session")
		return
	}
	session["user"] = map[string]any{"id": id, "email": email, "created_at": now}
	respond(w, 201, session)
}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		host = r.RemoteAddr
	}
	if !loginLimit.allow("ip:"+host) || !loginLimit.allow("email:"+email) {
		fail(w, 429, "too many attempts; retry later")
		return
	}
	rows, err := a.DB.Query(`SELECT id,email,password_hash FROM users WHERE email=?`, email)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	// Run password KDF even for unknown accounts to reduce account enumeration.
	stored := "pbkdf2-sha256$210000$00000000000000000000000000000000$0000000000000000000000000000000000000000000000000000000000000000"
	if len(rows) == 1 {
		stored = valString(rows[0], "password_hash")
	}
	correct := verifyPassword(input.Password, stored)
	if !correct || len(rows) != 1 {
		fail(w, 401, "invalid email or password")
		return
	}
	session, err := a.createSession(valString(rows[0], "id"))
	if err != nil {
		fail(w, 500, "cannot create session")
		return
	}
	respond(w, 200, session)
}
func (a *API) me(w http.ResponseWriter, r *http.Request) {
	id, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	rows, err := a.DB.Query(`SELECT id,email,created_at FROM users WHERE id=?`, id)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if len(rows) == 0 {
		fail(w, 401, "user unavailable")
		return
	}
	respond(w, 200, rows[0])
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err := a.DB.Exec(`DELETE FROM user_sessions WHERE token_hash=?`, tokenHash(token)); err != nil {
		fail(w, 500, "database error")
		return
	}
	w.WriteHeader(204)
}
func (a *API) myBaskets(w http.ResponseWriter, r *http.Request) {
	id, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	rows, err := a.DB.Query(`SELECT b.id,b.created_at,b.updated_at,COUNT(i.id) AS items FROM baskets b LEFT JOIN basket_items i ON i.basket_id=b.id WHERE b.user_id=? GROUP BY b.id ORDER BY b.updated_at DESC LIMIT 100`, id)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	respond(w, 200, map[string]any{"items": rows})
}
func (a *API) claimBasket(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	token := r.Header.Get("X-Basket-Token")
	if len(token) != 64 {
		fail(w, 401, "X-Basket-Token required to claim basket")
		return
	}
	rows, err := a.DB.Query(`SELECT token_hash,user_id FROM baskets WHERE id=?`, r.PathValue("id"))
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if len(rows) != 1 || rows[0]["user_id"] != nil || subtle.ConstantTimeCompare([]byte(valString(rows[0], "token_hash")), []byte(tokenHash(token))) != 1 {
		fail(w, 404, "anonymous basket not found or token invalid")
		return
	}
	// Atomic conditional UPDATE prevents ownership theft under racing claims.
	if err = a.DB.Exec(`UPDATE baskets SET user_id=?,token_hash='',updated_at=? WHERE id=? AND user_id IS NULL AND token_hash=?`, uid, time.Now().UTC().Format(time.RFC3339Nano), r.PathValue("id"), tokenHash(token)); err != nil {
		fail(w, 500, "database error")
		return
	}
	result, err := a.DB.Scalar("SELECT COUNT(*) FROM baskets WHERE id=? AND user_id=?", r.PathValue("id"), uid)
	if err != nil || result != 1 {
		fail(w, 409, "basket already claimed")
		return
	}
	respond(w, 200, map[string]any{"id": r.PathValue("id"), "owner": "account"})
}
