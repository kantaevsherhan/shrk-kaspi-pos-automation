package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSMSLoginSendsNationalPhone(t *testing.T) {
	for _, input := range []string{"7001234567", "77001234567", "+77001234567", "+7 (700) 123-45-67", "8 (700) 123-45-67"} {
		t.Run(input, func(t *testing.T) {
			a := testApp(t)
			a.flows["process"] = &authFlow{ProcessID: "process", Created: time.Now()}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				json.NewDecoder(r.Body).Decode(&payload)
				if field(payload, "data", "phoneNumber") != "7001234567" || field(payload, "meta", "sn") != "EnterPhoneNumber" {
					t.Error("SMS endpoint did not receive the national ten-digit phone")
				}
				// Current entrance UI can display the OTP as a dialog on the
				// phone view. The next process step identifies OTP entry.
				io.WriteString(w, `{"meta":{"sn":"ViewEnterOtp"},"view":{"code":"KPUniversalEnterPhoneNumber","main":{}}}`)
			}))
			defer upstream.Close()
			a.Cfg.EntranceURL = upstream.URL
			body, _ := json.Marshal(map[string]string{"processId": "process", "phoneNumber": input})
			response := request(a, "POST", "/api/auth/send-phone", a.Cfg.ServiceKeys["pos"], string(body))
			var result map[string]any
			json.Unmarshal(response.Body.Bytes(), &result)
			if response.Code != 200 || result["success"] != true || a.flows["process"].Phone != "77001234567" {
				t.Fatal("valid phone rejected or full session phone lost")
			}
		})
	}
}

func TestInvalidSMSPhonesDoNotReachKaspi(t *testing.T) {
	a := testApp(t)
	a.flows["process"] = &authFlow{ProcessID: "process", Created: time.Now()}
	for _, input := range []string{"", "700123456", "97001234567", "++77001234567", "770012345678", "700abc1234"} {
		body, _ := json.Marshal(map[string]string{"processId": "process", "phoneNumber": input})
		if result := request(a, "POST", "/api/auth/send-phone", a.Cfg.ServiceKeys["pos"], string(body)); result.Code != 400 {
			t.Errorf("invalid SMS phone accepted: %q, HTTP %d", input, result.Code)
		}
	}
}
