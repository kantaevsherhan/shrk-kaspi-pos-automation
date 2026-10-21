package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// An opt-in, isolated UI fixture. All Kaspi endpoints terminate at a local mock;
// it never loads .env, user state, Telegram credentials or a real Kaspi session.
func TestBrowserSmokeServer(t *testing.T) {
	if os.Getenv("KASPI_BROWSER_SMOKE") != "1" {
		t.Skip("set KASPI_BROWSER_SMOKE=1 to run the local browser fixture")
	}
	a := testApp(t)
	front, err := filepath.Abs("../../web/dist")
	if err != nil {
		t.Fatal(err)
	}
	a.Cfg.FrontendDir = front
	a.Cfg.SessionCheckInterval = 2 * time.Minute
	a.Cfg.InvoiceSendInterval = 7 * time.Second
	a.Cfg.TokenKey = bytes.Repeat([]byte{1}, 32)
	if err := a.createAdmin("smoke-owner", "smoke-test-only-password-2026"); err != nil {
		t.Fatal(err)
	}
	a.session = &KaspiSession{TokenSN: "mock-token", Secret: bytes.Repeat([]byte{2}, 32), ProfileID: "42", OrgName: "Тестовый магазин", Phone: "77001234567"}
	a.monitor.Status = "connected"
	a.monitor.LastChecked = time.Now()
	var mu sync.Mutex
	invoiceStatus := "RemotePaymentCreated"
	qrChecks := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var data any = map[string]any{}
		switch r.URL.Path {
		case "/api/v1/entrance/step":
			var input map[string]any
			json.NewDecoder(r.Body).Decode(&input)
			view, step := "KPUniversalEnterPhoneNumber", "EnterPhoneNumber"
			if field(input, "meta", "sn") == "EnterPhoneNumber" {
				view, step = "KPEnterLoginPassword", "ViewEnterLoginPassword"
				if field(input, "data", "phoneNumber") != "7001234567" {
					t.Error("masked phone was not sent in national format")
				}
			}
			if field(input, "meta", "sn") == "ViewEnterLoginPassword" {
				view, step = "EnterOtp", "ViewEnterOtp"
			}
			if field(input, "meta", "sn") == "ViewEnterOtp" {
				view, step = "UniversalKaspiIdTakePhoto", "KaspiIdTakePhoto"
			}
			json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"pId": "smoke-process", "sn": step}, "view": map[string]any{"code": view}})
			return
		case "/v01/remote/client-info":
			data = map[string]any{"ClientName": "Тестовый клиент", "ClientStatus": "Active"}
		case "/v01/remote/create", "/v02/remote/details":
			data = map[string]any{"QrOperationId": 123, "Amount": 1250, "ClientName": "Тестовый клиент", "Status": invoiceStatus}
		case "/v01/remote/cancel":
			invoiceStatus = "RemotePaymentCanceled"
		case "/v01/remote/history":
			data = map[string]any{"Operations": []any{map[string]any{"QrOperationId": 123, "Amount": 1250, "Status": invoiceStatus, "ClientMobile": "77001234567"}}}
		case "/v01/qr-token/create":
			data = map[string]any{"QrOperationId": 456, "Amount": 2500, "QrToken": "https://qr.kaspi.kz/LOCAL-MOCK-NOT-A-PAYMENT", "Status": "QrTokenCreated"}
		case "/v02/kaspi-qr/status":
			qrChecks++
			status := "QrTokenScanned"
			if qrChecks >= 2 {
				status = "Processed"
			}
			data = map[string]any{"Status": status}
		case "/v02/history/operations":
			data = map[string]any{"Statistic": map[string]any{"SalesCount": 1, "SalesAmount": "2 500 ₸"}, "DailySets": []any{map[string]any{"Date": "2026-10-20", "Operations": []any{map[string]any{"Id": 789, "Amount": "2 500 ₸", "OperationMethod": 2, "OperationType": 0, "Status": "Processed", "ClientName": "Тестовый клиент"}}}}}
		case "/v01/kaspi-qr/operations/details":
			data = map[string]any{"QrOperationId": 456, "Amount": 2500, "AvailableReturnAmount": 1500, "ClientName": "Тестовый клиент", "Status": "Processed", "DynamicDetails": []any{map[string]any{"Order": 1, "Title": "Номер заказа", "Data": "Тест-42"}}, "Returns": []any{map[string]any{"Date": "2026-10-19", "Amount": 1000}}}
		case "/v01/kaspi-qr/history-pos-return":
			var input map[string]any
			json.NewDecoder(r.Body).Decode(&input)
			if input["QrOperationId"] != float64(456) || input["ReturnAmount"] != float64(1500) {
				t.Error("UI selected the wrong refund ID or amount")
			}
			data = map[string]any{"Status": "Processed"}
		default:
			t.Errorf("unhandled mock endpoint: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"StatusCode": 0, "Data": data})
	}))
	defer mock.Close()
	a.Cfg.QRPayURL = mock.URL
	a.Cfg.EntranceURL = mock.URL
	address := os.Getenv("KASPI_BROWSER_SMOKE_ADDR")
	if address == "" {
		address = "127.0.0.1:38473"
	}
	server := &http.Server{Addr: address, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { server.Close() })
	t.Log("isolated browser fixture: http://127.0.0.1:38473")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
