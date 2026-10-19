package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := &Config{StateDir: dir, FrontendDir: dir, QRPayURL: "http://unused", ServiceKeys: map[string]string{"pos": "abcdefghijklmnopqrstuvwxyz1234567890"}, PrivateKey: key, App: AppInfo{Version: "1", Build: "1", Platform: "iOS", PlatformVer: "18", Locale: "ru"}, Device: DeviceInfo{DeviceID: "device", InstallID: "install"}}
	return &App{Cfg: cfg, client: &http.Client{Timeout: time.Second}, flows: map[string]*authFlow{}, tracked: map[string]*TrackedPayment{}}
}
func request(a *App, method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	return rec
}

func TestTokenMacAndSessionEncryption(t *testing.T) {
	got := tokenMac("token-123", []byte("secret-secret-secret-secret-1234"), time.Unix(1700000000, 0))
	if got != "980971" {
		t.Fatalf("token MAC mismatch: %s", got)
	}
	key := make([]byte, 32)
	rand.Read(key)
	encoded, err := encrypt(key, []byte("private session"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decrypt(key, encoded)
	if err != nil || string(plain) != "private session" {
		t.Fatalf("session encryption roundtrip: %v", err)
	}
	key[0] ^= 1
	if _, err = decrypt(key, encoded); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestAPIAuthAndInvoice(t *testing.T) {
	a := testApp(t)
	key := a.Cfg.ServiceKeys["pos"]
	if err := os.WriteFile(filepath.Join(a.Cfg.FrontendDir, "index.html"), []byte("<h1>console</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := request(a, "GET", "/", "", ""); got.Code != 200 || !strings.Contains(got.Body.String(), "console") {
		t.Fatal("frontend root failed", got.Code, got.Body.String())
	}
	if got := request(a, "GET", "/health", "", ""); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := request(a, "GET", "/api/auth/session", "", ""); got.Code != 401 {
		t.Fatal(got.Code)
	}
	if got := request(a, "GET", "/api/auth/session", key, ""); got.Code != 200 || !strings.Contains(got.Body.String(), `"authenticated":false`) {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := request(a, "POST", "/api/invoice/create", key, `{"phoneNumber":"77001234567","amount":1000}`); got.Code != 401 {
		t.Fatal(got.Code)
	}
	kaspi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v01/remote/create" || r.Header.Get("X-Sign") == "" {
			t.Errorf("bad Kaspi request: %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["PhoneNumber"] != "77001234567" {
			t.Error(body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"StatusCode":0,"Data":{"QrOperationId":123,"Status":"RemotePaymentCreated"}}`)
	}))
	defer kaspi.Close()
	a.Cfg.QRPayURL = kaspi.URL
	a.session = &KaspiSession{TokenSN: "token", Secret: []byte("01234567890123456789012345678901"), ProfileID: "profile"}
	rec := request(a, "POST", "/api/invoice/create", key, `{"phoneNumber":"77001234567","amount":1000,"comment":"test"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if a.tracked["123"] == nil {
		t.Fatal("invoice not tracked")
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.StateDir, "tracked-payments.json")); err != nil {
		t.Fatal(err)
	}
	if got := request(a, "POST", "/api/invoice/create", key, `{"phoneNumber":"bad","amount":1000}`); got.Code != 400 {
		t.Fatal(got.Code)
	}
	if got := request(a, "GET", "/api/audit/requests", key, ""); got.Code != 404 {
		t.Fatal("audit route still present")
	}
}

func TestWebhookSignatureAndRetry(t *testing.T) {
	a := testApp(t)
	attempts := 0
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("hook-secret"))
		mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("X-Webhook-Signature") != expected {
			t.Error("bad webhook signature")
		}
		if !bytes.Contains(body, []byte(`"paymentId":"123"`)) {
			t.Error("wrong webhook body")
		}
		if attempts == 1 {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer hook.Close()
	a.deliverWebhook(context.Background(), Webhook{URL: hook.URL, Secret: "hook-secret"}, map[string]any{"paymentId": "123", "event": "payment.success"}, 1)
	if attempts != 1 || len(a.retries) != 1 {
		t.Fatal("failed webhook not queued")
	}
	a.retries[0].ExecuteAfter = time.Now().Add(-time.Second)
	a.processRetries(context.Background())
	if attempts != 2 || len(a.retries) != 0 {
		t.Fatal("retry not delivered")
	}
}

func TestLegacyPendingStateMigration(t *testing.T) {
	payments, err := readLegacyPayments([]byte(`{"123":{"paymentId":"123","type":"qr","status":"QrTokenCreated","createdAt":1700000000000,"meta":{"amount":2500,"qrToken":"https://qr.kaspi.kz/example"}}}`))
	if err != nil || payments["123"].Amount != 2500 || payments["123"].Created.UnixMilli() != 1700000000000 {
		t.Fatalf("payment migration failed: %v %+v", err, payments)
	}
	retries, err := readLegacyRetries([]byte(`[{"hook":{"url":"https://example.com","secret":"x"},"payload":{"event":"payment.success"},"attempt":2,"executeAfter":1700000000000}]`))
	if err != nil || len(retries) != 1 || retries[0].ExecuteAfter.UnixMilli() != 1700000000000 {
		t.Fatalf("retry migration failed: %v %+v", err, retries)
	}
}

func TestMigrateRootState(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "data")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked-payments.json"), []byte(`{"123":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateRootState(root, state); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(state, "tracked-payments.json"))
	if err != nil || string(raw) != `{"123":{}}` {
		t.Fatalf("state migration failed: %v", err)
	}
}
