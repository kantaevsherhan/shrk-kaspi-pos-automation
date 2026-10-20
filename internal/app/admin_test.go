package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func adminRequest(a *App, method, path, body, bearer string, cookie string, origin bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	if origin {
		r.Header.Set("Origin", "http://example.com")
	}
	r.Host = "example.com"
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

func TestAdminSetupLoginAndCookie(t *testing.T) {
	a := testApp(t)
	key := a.Cfg.ServiceKeys["pos"]
	if got := adminRequest(a, "GET", "/api/admin/status", "", "", "", false); got.Code != 200 || !strings.Contains(got.Body.String(), `"configured":false`) {
		t.Fatal(got.Code, got.Body.String())
	}
	setup := `{"username":"owner","password":"a-long-private-password"}`
	if got := adminRequest(a, "POST", "/api/admin/setup", setup, "", "", false); got.Code != 401 {
		t.Fatal(got.Code)
	}
	if got := adminRequest(a, "POST", "/api/admin/setup", setup, key, "", false); got.Code != 201 {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := adminRequest(a, "POST", "/api/admin/setup", setup, key, "", false); got.Code != 409 {
		t.Fatal(got.Code)
	}
	b, err := os.ReadFile(filepath.Join(a.Cfg.StateDir, "admin.json"))
	if err != nil || strings.Contains(string(b), "a-long-private-password") {
		t.Fatal("password was not stored as a hash", err)
	}
	if got := adminRequest(a, "POST", "/api/admin/login", setup, "", "", false); got.Code != 403 {
		t.Fatal(got.Code)
	}
	if got := adminRequest(a, "POST", "/api/admin/login", `{"username":"owner","password":"wrong-password"}`, "", "", true); got.Code != 401 {
		t.Fatal(got.Code)
	}
	login := adminRequest(a, "POST", "/api/admin/login", setup, "", "", true)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatal(login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("weak cookie: %+v", cookie)
	}
	if got := adminRequest(a, "GET", "/api/admin/me", "", "", cookie.String(), false); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := adminRequest(a, "GET", "/api/auth/session", "", "", cookie.String(), false); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := adminRequest(a, "POST", "/api/auth/init", "", "", cookie.String(), false); got.Code != 403 {
		t.Fatal(got.Code)
	}
	if got := adminRequest(a, "POST", "/api/admin/logout", "", "", cookie.String(), true); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := adminRequest(a, "GET", "/api/admin/me", "", "", cookie.String(), false); got.Code != 401 {
		t.Fatal(got.Code)
	}
	b = append([]byte(nil), b...)
	a.admin = nil
	if err := a.loadAdmin(); err != nil || !a.checkAdmin("owner", "a-long-private-password") {
		t.Fatal("persisted admin failed", err)
	}
}

func TestAdminLoginRateLimit(t *testing.T) {
	a := testApp(t)
	if err := a.createAdmin("owner", "a-long-private-password"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if got := adminRequest(a, "POST", "/api/admin/login", `{"username":"owner","password":"wrong"}`, "", "", true); got.Code != 401 {
			t.Fatal(got.Code)
		}
	}
	if got := adminRequest(a, "POST", "/api/admin/login", `{"username":"owner","password":"a-long-private-password"}`, "", "", true); got.Code != 429 {
		t.Fatal(got.Code)
	}
}
