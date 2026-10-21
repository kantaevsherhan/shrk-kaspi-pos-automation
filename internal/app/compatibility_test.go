package app

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Contracts transcribed from d554ab2:src/routes/{invoice,qr,history,refund}.js,
// the last Node implementation before the Go rewrite. Keep numeric-ID callers
// covered: the original dashboard sends numbers, while the Vue UI sends strings.
func TestOriginalPaymentContracts(t *testing.T) {
	cases := []struct{ name, method, route, input, upstreamMethod, upstreamPath, upstreamBody string }{
		{"client", "GET", "/api/invoice/client-info?phoneNumber=77001234567", "", "GET", "/v01/remote/client-info?phoneNumber=77001234567", ""},
		{"invoice", "POST", "/api/invoice/create", `{"phoneNumber":"77001234567","amount":1250.50,"comment":"Заказ <42>"}`, "POST", "/v01/remote/create", `{"PhoneNumber":"77001234567","Amount":1250.50,"Comment":"Заказ <42>"}`},
		{"invoice amount string", "POST", "/api/invoice/create", `{"phoneNumber":"77001234567","amount":"1250.50"}`, "POST", "/v01/remote/create", `{"PhoneNumber":"77001234567","Amount":1250.50,"Comment":""}`},
		{"invoice details", "GET", "/api/invoice/details?operationId=123", "", "GET", "/v02/remote/details?operationId=123", ""},
		{"cancel numeric", "POST", "/api/invoice/cancel", `{"operationId":123}`, "POST", "/v01/remote/cancel", `{"qrOperationId":123}`},
		{"cancel string", "POST", "/api/invoice/cancel", `{"operationId":"123"}`, "POST", "/v01/remote/cancel", `{"qrOperationId":123}`},
		{"decimal ID with zeros", "POST", "/api/invoice/cancel", `{"operationId":"000123"}`, "POST", "/v01/remote/cancel", `{"qrOperationId":123}`},
		{"invoices", "POST", "/api/invoice/history", "", "POST", "/v01/remote/history", `{"MaxResult":20}`},
		{"qr", "POST", "/api/qr/create", `{"amount":2500}`, "POST", "/v01/qr-token/create", `{"PaymentAmount":2500,"DeviceInterface":"Pos","Latitude":43.204643483375889,"Longitude":76.891962364115912}`},
		{"qr coordinates", "POST", "/api/qr/create", `{"amount":"2500","latitude":51.1,"longitude":71.4}`, "POST", "/v01/qr-token/create", `{"PaymentAmount":2500,"DeviceInterface":"Pos","Latitude":51.1,"Longitude":71.4}`},
		{"qr status", "GET", "/api/qr/status?qrOperationId=123", "", "GET", "/v02/kaspi-qr/status?qrOperationId=123", ""},
		{"history", "POST", "/api/history/operations", `{"endDate":"2026-10-20T23:59:59.000+0500","lastTransactionDate":"older","statementPeriodCode":1}`, "POST", "/v02/history/operations", `{"EndDate":"2026-10-20T23:59:59.000+0500","LastTransactionDate":"older","StatementPeriodCode":1}`},
		{"operation numeric", "POST", "/api/history/details", `{"id":123,"operationMethod":2}`, "POST", "/v01/kaspi-qr/operations/details", `{"Id":123,"OperationMethod":2}`},
		{"operation string", "POST", "/api/history/details", `{"id":"123"}`, "POST", "/v01/kaspi-qr/operations/details", `{"Id":123,"OperationMethod":0}`},
		{"refund numeric", "POST", "/api/refund/create", `{"qrOperationId":123,"returnAmount":10}`, "POST", "/v01/kaspi-qr/history-pos-return", `{"QrOperationId":123,"ReturnAmount":10,"DeviceInterface":"Pos"}`},
		{"refund string", "POST", "/api/refund/create", `{"qrOperationId":"123","returnAmount":"10.50"}`, "POST", "/v01/kaspi-qr/history-pos-return", `{"QrOperationId":123,"ReturnAmount":10.50,"DeviceInterface":"Pos"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, _ := io.ReadAll(r.Body)
				if r.Method != tc.upstreamMethod || r.URL.RequestURI() != tc.upstreamPath {
					t.Errorf("upstream request: %s %s", r.Method, r.URL.RequestURI())
				}
				if !reflect.DeepEqual(decodeContractJSON(t, body), decodeContractJSON(t, []byte(tc.upstreamBody))) {
					t.Errorf("upstream body mismatch: %s", body)
				}
				verifyOriginalSignature(t, &a.Cfg.PrivateKey.PublicKey, r, body)
				if r.Header.Get("X-PI") != "profile" || r.Header.Get("X-Kb-TokenSn") != "test-token" {
					t.Error("Kaspi session headers missing")
				}
				io.WriteString(w, `{"StatusCode":0,"Data":{"QrOperationId":123,"QrToken":"https://qr.kaspi.kz/example","Status":"QrTokenCreated"}}`)
			}))
			defer upstream.Close()
			a.Cfg.QRPayURL = upstream.URL
			a.session = &KaspiSession{TokenSN: "test-token", Secret: []byte("01234567890123456789012345678901"), ProfileID: "profile"}
			got := request(a, tc.method, tc.route, a.Cfg.ServiceKeys["pos"], tc.input)
			if got.Code != 200 || calls != 1 {
				t.Fatalf("API response %d (%d upstream calls): %s", got.Code, calls, got.Body.String())
			}
			if tc.route == "/api/qr/create" && (!strings.Contains(got.Body.String(), `"QrOriginalToken":"https://qr.kaspi.kz/example"`) || !strings.Contains(got.Body.String(), `"QrToken":"https://pay.kaspi.kz/pay/example"`)) {
				t.Fatal("QR link compatibility failed")
			}
		})
	}
}

func decodeContractJSON(t *testing.T, b []byte) any {
	t.Helper()
	if len(b) == 0 {
		return nil
	}
	var result any
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// Independent verification of the Node computeXSign contract: SHA256 of the
// canonical header/body bytes, signed with ECDSA-SHA256 (therefore a second hash).
func verifyOriginalSignature(t *testing.T, public *ecdsa.PublicKey, r *http.Request, body []byte) {
	t.Helper()
	lines := []string{}
	for _, name := range strings.Split(r.Header.Get("X-SH"), ",") {
		value := r.Header.Get(name)
		if name == "url" {
			value = strings.ToLower("http://" + r.Host + r.URL.RequestURI())
		}
		lines = append(lines, strings.ToLower(name)+":"+value)
	}
	canonical := strings.Join(lines, "\n")
	if len(body) > 0 {
		canonical += "\n" + string(body)
	}
	first := sha256.Sum256([]byte(canonical))
	second := sha256.Sum256(first[:])
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Sign"))
	if err != nil || !ecdsa.VerifyASN1(public, second[:], sig) {
		t.Error("signature differs from original Node contract")
	}
}

func TestOriginalSMSLoginAndEncryptedRestart(t *testing.T) {
	a := testApp(t)
	a.Cfg.TokenKey = bytes.Repeat([]byte{7}, 32)
	remote, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	remoteDER, _ := x509.MarshalPKIXPublicKey(remote.PublicKey())
	var shared []byte
	steps, finished, organizations := 0, 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var input map[string]any
		if err := json.Unmarshal(body, &input); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/api/v1/entrance/step":
			steps++
			switch steps {
			case 1:
				if field(input, "data", "auth") != "2" || input["actType"] != "Success" {
					t.Error("entrance init payload changed")
				}
				http.SetCookie(w, &http.Cookie{Name: "user_token", Value: "first-cookie"})
				io.WriteString(w, `{"meta":{"pId":"process-1"},"view":{"code":"EnterPhoneNumber"}}`)
			case 2:
				if field(input, "meta", "sn") != "EnterPhoneNumber" || field(input, "data", "phoneNumber") != "7001234567" || !strings.Contains(r.Header.Get("Cookie"), "user_token=first-cookie") {
					t.Error("phone step differs from Node")
				}
				http.SetCookie(w, &http.Cookie{Name: "user_token", Value: "second-cookie"})
				io.WriteString(w, `{"view":{"code":"EnterOtp"}}`)
			case 3:
				if field(input, "meta", "sn") != "ViewEnterOtp" || field(input, "data", "userOtp") != "1234" || !strings.Contains(r.Header.Get("Cookie"), "user_token=second-cookie") {
					t.Error("OTP step differs from Node")
				}
				io.WriteString(w, `{"data":{"type":"kpDeviceRegistration"}}`)
			default:
				t.Error("unexpected auth step")
			}
		case "/api/v1/kpentrance/finish":
			finished++
			verifyOriginalSignature(t, &a.Cfg.PrivateKey.PublicKey, r, body)
			signed := stringValue(field(input, "signed", "data"))
			sig, _ := base64.StdEncoding.DecodeString(stringValue(field(input, "signed", "sign")))
			hash := sha256.Sum256([]byte(signed))
			if !ecdsa.VerifyASN1(&a.Cfg.PrivateKey.PublicKey, hash[:], sig) {
				t.Error("signed login data invalid")
			}
			der, err := base64.StdEncoding.DecodeString(stringValue(field(input, "guard", "x509")))
			if err != nil {
				t.Error(err)
				return
			}
			public, err := x509.ParsePKIXPublicKey(der)
			if err != nil {
				t.Error(err)
				return
			}
			ec, ok := public.(*ecdsa.PublicKey)
			if !ok {
				t.Error("unexpected ECDH key format")
				return
			}
			ep, _ := ec.ECDH()
			shared, err = remote.ECDH(ep)
			if err != nil {
				t.Error(err)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"tokenSN": "secure-token", "x509": base64.StdEncoding.EncodeToString(remoteDER)}})
		case "/v08/organizations/org-context-otp":
			organizations++
			verifyOriginalSignature(t, &a.Cfg.PrivateKey.PublicKey, r, body)
			io.WriteString(w, `{"StatusCode":0,"Data":{"Current":{"ProfileId":42,"OrganizationId":12,"OrganizationName":"Test shop"},"PhoneNumber":"77001234567"}}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	a.Cfg.EntranceURL, a.Cfg.MTokenURL = upstream.URL, upstream.URL
	key := a.Cfg.ServiceKeys["pos"]
	for _, tc := range []struct{ path, body string }{
		{"/api/auth/init", `{}`},
		{"/api/auth/send-phone", `{"processId":"process-1","phoneNumber":"77001234567"}`},
		{"/api/auth/verify-otp", `{"processId":"process-1","otp":"1234"}`},
	} {
		got := request(a, "POST", tc.path, key, tc.body)
		if got.Code != 200 || !strings.Contains(got.Body.String(), `"success":true`) {
			t.Fatalf("%s: %d %s", tc.path, got.Code, got.Body.String())
		}
	}
	if steps != 3 || finished != 1 || organizations != 1 {
		t.Fatal("incomplete SMS workflow")
	}
	s := a.currentSession()
	if s == nil || s.ProfileID != "42" || !bytes.Equal(s.Secret, shared) {
		t.Fatal("secure session or organization context lost")
	}
	raw, _ := os.ReadFile(a.sessionPath())
	if bytes.Contains(raw, []byte("secure-token")) {
		t.Fatal("Kaspi token stored in plaintext")
	}
	restarted, err := New(a.Cfg)
	if err != nil || restarted.currentSession() == nil || !bytes.Equal(restarted.currentSession().Secret, shared) {
		t.Fatalf("session restart failed: %v", err)
	}
	for _, method := range []string{"GET", "POST"} {
		got := request(restarted, method, "/api/auth/session", key, "")
		if got.Code != 200 || !strings.Contains(got.Body.String(), `"authenticated":true`) {
			t.Fatalf("original session route %s: %d", method, got.Code)
		}
	}
	if got := request(restarted, "POST", "/api/auth/logout", key, ""); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if _, err := os.Stat(restarted.sessionPath()); !os.IsNotExist(err) {
		t.Fatal("logout retained Kaspi credentials")
	}
}

func TestKaspiHTTPFailureAndMalformedStatusAreNotSuccess(t *testing.T) {
	a := testApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"StatusCode":0}`)
	}))
	defer upstream.Close()
	a.Cfg.QRPayURL = upstream.URL
	active, _, err := a.checkSession(context.Background(), &KaspiSession{})
	if err == nil || active {
		t.Fatal("HTTP 401 was accepted as a valid session")
	}
	for _, code := range []any{nil, "invalid", "0", false, map[string]any{}} {
		if kaspiOK(map[string]any{"StatusCode": code}) {
			t.Fatalf("invalid status accepted: %#v", code)
		}
	}
}

func TestInvalidInputsDoNotReachKaspi(t *testing.T) {
	a := testApp(t)
	a.session = &KaspiSession{}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/invoice/client-info", ""},
		{"GET", "/api/invoice/details?operationId=bad", ""},
		{"GET", "/api/qr/status", ""},
		{"POST", "/api/invoice/cancel", `{"operationId":1.5}`},
		{"POST", "/api/invoice/cancel", `{"operationId":-1}`},
		{"POST", "/api/history/details", `{"id":true}`},
		{"POST", "/api/refund/create", `{"qrOperationId":123,"returnAmount":null}`},
		{"POST", "/api/qr/create", `{"amount":"NaN"}`},
		{"POST", "/api/qr/create", `{"amount":true}`},
		{"POST", "/api/invoice/create", `{"phoneNumber":"77001234567","amount":-10}`},
		{"POST", "/api/auth/send-phone", `{"processId":"missing","phoneNumber":"77001234567"}`},
		{"POST", "/api/auth/verify-otp", `{"processId":"missing","otp":"1234"}`},
	} {
		if got := request(a, tc.method, tc.path, a.Cfg.ServiceKeys["pos"], tc.body); got.Code != 400 {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, got.Code, got.Body.String())
		}
	}
}
