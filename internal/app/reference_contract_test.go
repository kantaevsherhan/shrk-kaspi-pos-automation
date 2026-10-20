package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Values evaluated with the working Python integration's pure
// _compute_token_sn_mac function, using synthetic inputs only. The reference
// application itself is never imported, run or needed by this test suite.
func TestWorkingReferenceMACVectors(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}
	for _, tc := range []struct {
		token string
		at    int64
		want  string
	}{
		{"test-token", 1700000000, "127518"},
		{"test-token", 1700000009, "127518"},
		{"test-token", 1700000010, "749668"},
		{"0123456789abcdef0123456789abcdefEXTRA", 1700000000, "961069"},
		{"0123456789abcdef0123456789abcdef", 1700000000, "961069"},
		{"test-token", 1800000000, "729861"},
	} {
		if got := tokenMac(tc.token, secret, time.Unix(tc.at, 0)); got != tc.want {
			t.Errorf("reference MAC mismatch at %d: got %s, want %s", tc.at, got, tc.want)
		}
	}
}

// Kaspi paths, field names, limits and date filters used by kaspi_server.py:
// api_create_invoice, api_history, _check_token_alive and v02 reconciliation.
// Local API names intentionally remain those of our independent service.
func TestWorkingReferencePaymentRequests(t *testing.T) {
	for _, tc := range []struct{ name, method, route, input, path, body string }{
		{"formatted client", "GET", "/api/invoice/client-info?phoneNumber=" + url.QueryEscape("+7 (700) 123-45-67"), "", "/v01/remote/client-info?phoneNumber=77001234567", ""},
		{"formatted invoice", "POST", "/api/invoice/create", `{"phoneNumber":" 8 (700) 123-45-67 ","amount":1250.5,"comment":"Заказ 42"}`, "/v01/remote/create", `{"PhoneNumber":"77001234567","Amount":1250.5,"Comment":"Заказ 42"}`},
		{"invoice history limit", "POST", "/api/invoice/history", `{"maxResult":600}`, "/v01/remote/history", `{"MaxResult":600}`},
		{"invoice history defaults", "POST", "/api/invoice/history", `{}`, "/v01/remote/history", `{"MaxResult":20}`},
		{"day history with pagination", "POST", "/api/history/operations", `{"startDate":"2026-10-20T00:00:00.000+0500","endDate":"2026-10-20T23:59:59.000+0500","pageSize":200,"lastTransactionDate":"cursor","paymentType":0,"statementPeriodCode":0}`, "/v02/history/operations", `{"StartDate":"2026-10-20T00:00:00.000+0500","EndDate":"2026-10-20T23:59:59.000+0500","PageSize":200,"LastTransactionDate":"cursor","PaymentType":0,"StatementPeriodCode":0}`},
		{"session probe", "GET", "/api/session/check", "", "/v01/remote/history", `{"MaxResult":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.session = &KaspiSession{TokenSN: "synthetic-token", Secret: bytes.Repeat([]byte{3}, 32), ProfileID: "42"}
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, _ := io.ReadAll(r.Body)
				method := "POST"
				if tc.body == "" {
					method = "GET"
				}
				if r.Method != method || r.URL.RequestURI() != tc.path || !reflect.DeepEqual(decodeContractJSON(t, body), decodeContractJSON(t, []byte(tc.body))) {
					t.Errorf("unexpected upstream request: %s %s %s", r.Method, r.URL.RequestURI(), body)
				}
				verifyOriginalSignature(t, &a.Cfg.PrivateKey.PublicKey, r, body)
				if r.Header.Get("X-Device-ID") != a.Cfg.Device.DeviceID || r.Header.Get("X-Install-ID") != a.Cfg.Device.InstallID || r.Header.Get("X-PI") != "42" || r.Header.Get("X-Kb-TokenSn") != "synthetic-token" {
					t.Error("request did not use the SMS session and its own device identity")
				}
				io.WriteString(w, `{"StatusCode":0,"Data":{"DailySets":[{"Operations":[{"Id":987,"Status":"Processed","Amount":"1 250 ₸"}]}]}}`)
			}))
			defer upstream.Close()
			a.Cfg.QRPayURL = upstream.URL
			got := request(a, tc.method, tc.route, a.Cfg.ServiceKeys["pos"], tc.input)
			if got.Code != 200 || calls != 1 {
				t.Fatalf("response %d, calls %d: %s", got.Code, calls, got.Body.String())
			}
			if len(a.activePayments()) != 0 {
				t.Fatal("history response persisted a completed payment")
			}
		})
	}
}

func TestFormattedSMSPhone(t *testing.T) {
	a := testApp(t)
	a.flows["process"] = &authFlow{ProcessID: "process", Created: time.Now()}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if field(body, "data", "phoneNumber") != "77001234567" {
			t.Error("SMS request did not normalize the phone")
		}
		io.WriteString(w, `{"view":{"code":"EnterOtp"}}`)
	}))
	defer upstream.Close()
	a.Cfg.EntranceURL = upstream.URL
	got := request(a, "POST", "/api/auth/send-phone", a.Cfg.ServiceKeys["pos"], `{"processId":"process","phoneNumber":"+7 (700) 123-45-67"}`)
	if got.Code != 200 || a.flows["process"].Phone != "77001234567" {
		t.Fatal("formatted SMS phone rejected or stored without normalization", got.Code)
	}
}

func TestReferenceInvalidInputDoesNotReachKaspi(t *testing.T) {
	a := testApp(t)
	a.session = &KaspiSession{}
	for _, tc := range []struct{ route, body string }{
		{"/api/invoice/create", `{"phoneNumber":"7e7001234567","amount":100}`},
		{"/api/invoice/create", `{"phoneNumber":"++77001234567","amount":100}`},
		{"/api/invoice/history", `{"maxResult":0}`},
		{"/api/invoice/history", `{"maxResult":1001}`},
		{"/api/invoice/history", `{"maxResult":`},
		{"/api/history/operations", `{"endDate":"2026-10-20","pageSize":0}`},
		{"/api/history/operations", `{"endDate":"2026-10-20","pageSize":1001}`},
	} {
		if got := request(a, "POST", tc.route, a.Cfg.ServiceKeys["pos"], tc.body); got.Code != 400 {
			t.Errorf("invalid input: %s: HTTP %d", tc.route, got.Code)
		}
	}
}

func TestPendingInvoiceResumesAfterOutageAndRestart(t *testing.T) {
	for _, failure := range []string{"network", "legacy_expired", "expired", "invalid_json"} {
		t.Run(failure, func(t *testing.T) {
			a := testApp(t)
			a.Cfg.TokenKey = bytes.Repeat([]byte{4}, 32)
			if err := a.saveSession(&KaspiSession{TokenSN: "synthetic-token", Secret: bytes.Repeat([]byte{2}, 32), ProfileID: "42"}); err != nil {
				t.Fatal(err)
			}
			recovered, calls, delivered := false, 0, 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.RequestURI() != "/v02/remote/details?operationId=123" {
					t.Error("polled wrong invoice endpoint")
				}
				if recovered {
					io.WriteString(w, `{"StatusCode":0,"Data":{"Status":"Processed","OrderNumber":"test-42"}}`)
					return
				}
				switch failure {
				case "network":
					w.WriteHeader(502)
				case "legacy_expired":
					io.WriteString(w, `{"StatusCode":-10001}`)
				case "expired":
					io.WriteString(w, `{"StatusCode":-101001}`)
				default:
					io.WriteString(w, "not json")
				}
			}))
			defer upstream.Close()
			a.Cfg.QRPayURL = upstream.URL
			hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				json.NewDecoder(r.Body).Decode(&payload)
				if payload["event"] != "payment.success" || payload["paymentId"] != "123" {
					t.Error("outage was treated as a final payment result")
				}
				delivered++
				w.WriteHeader(204)
			}))
			defer hook.Close()
			config, _ := json.Marshal([]Webhook{{URL: hook.URL, Secret: "synthetic-hook-secret", Events: []string{"payment.success", "payment.lost"}}})
			if err := os.WriteFile(filepath.Join(a.Cfg.StateDir, "webhooks.json"), config, 0600); err != nil {
				t.Fatal(err)
			}
			a.track(&TrackedPayment{ID: "123", Type: "invoice", Status: "RemotePaymentCreated", ProfileID: "42", Amount: 100, Created: time.Now().Add(-25 * time.Hour)})
			for i := 0; i < 12; i++ {
				a.mu.Lock()
				if p := a.tracked["123"]; p != nil {
					p.NextCheck = time.Time{}
				}
				a.mu.Unlock()
				a.pollOnce(context.Background())
			}
			if len(a.activePayments()) != 1 || delivered != 0 || calls != 12 {
				t.Fatalf("invoice lost during outage: pending=%d deliveries=%d calls=%d", len(a.activePayments()), delivered, calls)
			}
			a.pollOnce(context.Background())
			if calls != 12 {
				t.Fatal("retry backoff was ignored")
			}
			restored, err := New(a.Cfg)
			if err != nil || len(restored.activePayments()) != 1 || restored.activePayments()[0].NextCheck.IsZero() {
				t.Fatalf("pending invoice/backoff did not survive restart: %v", err)
			}
			restored.tracked["123"].NextCheck = time.Time{}
			if err := restored.saveSession(&KaspiSession{TokenSN: "other-account", Secret: bytes.Repeat([]byte{6}, 32), ProfileID: "99"}); err != nil {
				t.Fatal(err)
			}
			restored.pollOnce(context.Background())
			if calls != 12 || len(restored.activePayments()) != 1 {
				t.Fatal("invoice was queried with another organization's session or discarded")
			}
			// Reconnect the same organization and confirm an invoice older than
			// 24h using details; remote history may have already omitted it.
			if err := restored.saveSession(&KaspiSession{TokenSN: "new-synthetic-token", Secret: bytes.Repeat([]byte{5}, 32), ProfileID: "42"}); err != nil {
				t.Fatal(err)
			}
			restored.tracked["123"].NextCheck = time.Time{}
			recovered = true
			restored.pollOnce(context.Background())
			if len(restored.activePayments()) != 0 || delivered != 1 {
				t.Fatal("paid invoice was not reconciled after restart and reconnection")
			}
		})
	}
}
