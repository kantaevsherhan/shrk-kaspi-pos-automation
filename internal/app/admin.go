package app

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const adminCookie = "kaspi_pos_admin"
const adminIterations = 600000

type adminAccount struct {
	Username   string `json:"username"`
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
	Iterations int    `json:"iterations"`
}
type loginFailure struct {
	Count int
	Until time.Time
}

func (a *App) adminPath() string { return filepath.Join(a.Cfg.StateDir, "admin.json") }

func (a *App) loadAdmin() error {
	b, err := os.ReadFile(a.adminPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record adminAccount
	if err := json.Unmarshal(b, &record); err != nil {
		return err
	}
	salt, err := base64.RawStdEncoding.DecodeString(record.Salt)
	if err != nil {
		return errors.New("invalid admin credentials file")
	}
	hash, err := base64.RawStdEncoding.DecodeString(record.Hash)
	if err != nil || len(salt) != 16 || len(hash) != 32 || record.Iterations != adminIterations || !validUsername(record.Username) {
		return errors.New("invalid admin credentials file")
	}
	a.admin = &record
	return nil
}

func validUsername(name string) bool {
	if len(name) < 3 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func (a *App) createAdmin(name, password string) error {
	if !validUsername(name) || len(password) < 12 || len(password) > 256 {
		return errors.New("username must be 3-64 letters/numbers and password 12-256 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, adminIterations, 32)
	if err != nil {
		return err
	}
	record := adminAccount{Username: name, Salt: base64.RawStdEncoding.EncodeToString(salt), Hash: base64.RawStdEncoding.EncodeToString(hash), Iterations: adminIterations}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.admin != nil {
		return errors.New("administrator already configured")
	}
	if err := writeJSON(a.adminPath(), record); err != nil {
		return err
	}
	a.admin = &record
	return nil
}

func (a *App) checkAdmin(name, password string) bool {
	a.mu.RLock()
	record := a.admin
	a.mu.RUnlock()
	if record == nil {
		return false
	}
	salt, _ := base64.RawStdEncoding.DecodeString(record.Salt)
	expected, _ := base64.RawStdEncoding.DecodeString(record.Hash)
	actual, err := pbkdf2.Key(sha256.New, password, salt, record.Iterations, 32)
	return err == nil && subtle.ConstantTimeCompare([]byte(name), []byte(record.Username)) == 1 && subtle.ConstantTimeCompare(actual, expected) == 1
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
func (a *App) loginAllowed(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.loginFailures[ip]
	return f.Count < 5 || time.Now().After(f.Until)
}
func (a *App) loginFailed(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loginFailures == nil {
		a.loginFailures = map[string]loginFailure{}
	}
	f := a.loginFailures[ip]
	if time.Now().After(f.Until) {
		f.Count = 0
	}
	f.Count++
	f.Until = time.Now().Add(15 * time.Minute)
	a.loginFailures[ip] = f
}
func (a *App) loginSucceeded(ip string) {
	a.mu.Lock()
	delete(a.loginFailures, ip)
	a.mu.Unlock()
}
func (a *App) newAdminSession(w http.ResponseWriter, r *http.Request) error {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	raw := base64.RawURLEncoding.EncodeToString(token)
	hash := sha256.Sum256([]byte(raw))
	a.mu.Lock()
	if a.adminSessions == nil {
		a.adminSessions = map[[32]byte]time.Time{}
	}
	for key, expiry := range a.adminSessions {
		if time.Now().After(expiry) {
			delete(a.adminSessions, key)
		}
	}
	a.adminSessions[hash] = time.Now().Add(12 * time.Hour)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: raw, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.Cfg.SecureCookies || r.TLS != nil, Expires: time.Now().Add(12 * time.Hour)})
	return nil
}
func (a *App) adminAuthenticated(r *http.Request) bool {
	c, err := r.Cookie(adminCookie)
	if err != nil {
		return false
	}
	hash := sha256.Sum256([]byte(c.Value))
	a.mu.RLock()
	expiry, ok := a.adminSessions[hash]
	a.mu.RUnlock()
	return ok && time.Now().Before(expiry)
}
func (a *App) endAdminSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(adminCookie); err == nil {
		hash := sha256.Sum256([]byte(c.Value))
		a.mu.Lock()
		delete(a.adminSessions, hash)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.Cfg.SecureCookies || r.TLS != nil, MaxAge: -1})
}
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
}
