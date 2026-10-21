package app

import (
	"bytes"
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

func TestKaspiPasswordStepBeforeSMS(t *testing.T) {
	a := testApp(t)
	a.flows["process"] = &authFlow{ProcessID: "process", Created: time.Now()}
	const goodPassword = "synthetic-bank-password"
	passwordCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		json.NewDecoder(r.Body).Decode(&input)
		switch field(input, "meta", "sn") {
		case "EnterPhoneNumber":
			http.SetCookie(w, &http.Cookie{Name: "user_token", Value: "phone-step-cookie"})
			// Observed live response: this is a password request, not an SMS error.
			io.WriteString(w, `{"meta":{"pId":"password-process","sn":"ViewEnterLoginPassword"},"view":{"code":"KPEnterLoginPassword"},"type":"View"}`)
		case "ViewEnterLoginPassword":
			passwordCalls++
			if field(input, "meta", "pId") != "password-process" || !strings.Contains(r.Header.Get("Cookie"), "user_token=phone-step-cookie") || !strings.Contains(r.Header.Get("Referer"), "/enter-login-password?") {
				t.Error("password step lost provider process, cookie or referer")
			}
			if field(input, "data", "password") != goodPassword {
				t.Error("password was not forwarded in the official data.password field")
			}
			if passwordCalls == 1 {
				json.NewEncoder(w).Encode(map[string]any{"actType": "Alarm", "error": map[string]any{"code": "InvalidPassword", "label": "Rejected " + goodPassword}, "data": map[string]any{"password": goodPassword}})
				return
			}
			io.WriteString(w, `{"meta":{"sn":"ViewEnterOtp"},"view":{"code":"EnterOtp","main":"KPEnterLoginPassword"}}`)
		default:
			t.Error("unexpected auth step")
		}
	}))
	defer upstream.Close()
	a.Cfg.EntranceURL = upstream.URL
	decodeResult := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		if response.Code != 200 {
			t.Fatalf("auth route returned HTTP %d", response.Code)
		}
		if bytes.Contains(response.Body.Bytes(), []byte(goodPassword)) {
			t.Fatal("submitted password leaked in response")
		}
		var result map[string]any
		json.Unmarshal(response.Body.Bytes(), &result)
		return result
	}
	key := a.Cfg.ServiceKeys["pos"]
	result := decodeResult(request(a, "POST", "/api/auth/send-phone", key, `{"processId":"process","phoneNumber":"77001234567"}`))
	if result["success"] != true || result["nextStep"] != "password" || result["smsSent"] != false {
		t.Fatal("password request was misreported as SMS sent or an error")
	}
	body, _ := json.Marshal(map[string]string{"processId": "process", "password": goodPassword})
	result = decodeResult(request(a, "POST", "/api/auth/verify-password", key, string(body)))
	if result["success"] != false || result["errorCode"] != "InvalidPassword" {
		t.Fatal("provider password error was hidden")
	}
	result = decodeResult(request(a, "POST", "/api/auth/verify-password", key, string(body)))
	if result["success"] != true || result["nextStep"] != "otp" || result["smsSent"] != true {
		t.Fatal("password acceptance did not advance to OTP")
	}
	if a.flows["process"].View != "KPEnterLoginPassword" {
		t.Fatal("OTP dialog lost its parent page for the next request")
	}
	if response := request(a, "POST", "/api/auth/verify-password", key, string(body)); response.Code != 400 || passwordCalls != 2 {
		t.Fatal("password could be resubmitted after advancing to OTP")
	}
	files, _ := os.ReadDir(a.Cfg.StateDir)
	for _, file := range files {
		data, _ := os.ReadFile(filepath.Join(a.Cfg.StateDir, file.Name()))
		if bytes.Contains(data, []byte(goodPassword)) {
			t.Fatal("Kaspi password persisted to disk")
		}
	}
}

func TestKaspiPasswordCannotBeSubmittedBeforeRequested(t *testing.T) {
	a := testApp(t)
	a.flows["process"] = &authFlow{ProcessID: "process", Created: time.Now(), Step: "EnterPhoneNumber"}
	for _, body := range []string{`{"processId":"process","password":"synthetic"}`, `{"processId":"process","password":""}`} {
		if response := request(a, "POST", "/api/auth/verify-password", a.Cfg.ServiceKeys["pos"], body); response.Code != 400 {
			t.Errorf("password submitted out of sequence: HTTP %d", response.Code)
		}
	}
}
