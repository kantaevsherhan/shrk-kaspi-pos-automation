package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestKaspiIDPhotoNeverCompletesLogin(t *testing.T) {
	for _, view := range []string{"UniversalKaspiIdTakePhoto", "KaspiIdTakePhoto"} {
		for _, variant := range []string{"plain", "alarm", "otp", "registration"} {
			t.Run(view+"/"+variant, func(t *testing.T) {
				a := testApp(t)
				flow := &authFlow{ProcessID: "process", Created: time.Now()}
				a.flows["process"] = flow
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.WriteHeader(http.StatusInternalServerError)
				}))
				defer upstream.Close()
				a.Cfg.EntranceURL = upstream.URL
				result := map[string]any{
					"view": map[string]any{"code": view},
					"data": map[string]any{"verificationId": "private-verification", "desc": "private-provider-data"},
				}
				switch variant {
				case "alarm":
					result["actType"] = "Alarm"
					result["isClosed"] = true
					result["error"] = map[string]any{"label": "generic failure"}
				case "otp":
					result["meta"] = map[string]any{"sn": "ViewEnterOtp"}
					result["view"].(map[string]any)["main"] = "KPEnterLoginPassword"
				case "registration":
					result["data"].(map[string]any)["type"] = "kpDeviceRegistration"
				}
				response, err := a.authProgress(context.Background(), "process", flow, result)
				if err != nil || response["success"] != false || response["nextStep"] != "kaspi_id" || response["smsSent"] != false || response["supported"] != false || response["requiresUserAction"] != true || response["errorCode"] != "KASPI_ID_NATIVE_REQUIRED" {
					t.Fatal("photo challenge was not classified as requiring an unsupported native module")
				}
				if calls != 0 || flow.Finished || a.session != nil || a.flows["process"] != flow {
					t.Fatal("photo challenge unexpectedly attempted finish or created a session")
				}
				encoded, _ := json.Marshal(response)
				if strings.Contains(string(encoded), "private-") || response["desc"] != "" {
					t.Fatal("provider verification data leaked into the public response")
				}
				files, err := os.ReadDir(a.Cfg.StateDir)
				if err != nil || len(files) != 0 {
					t.Fatal("photo challenge persisted state before verification")
				}
			})
		}
	}
}

func TestOTPAdvancesToKaspiIDWithoutFinishing(t *testing.T) {
	a := testApp(t)
	a.flows["process"] = &authFlow{ProcessID: "process", Created: time.Now(), Step: "ViewEnterOtp"}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var input map[string]any
		json.NewDecoder(r.Body).Decode(&input)
		if r.URL.Path != "/api/v1/entrance/step" || field(input, "meta", "sn") != "ViewEnterOtp" {
			t.Error("unexpected request; photo verification must not call finish")
		}
		json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"sn": "KaspiIdTakePhoto"}, "view": map[string]any{"code": "UniversalKaspiIdTakePhoto"}, "data": map[string]any{"verificationId": "private-verification"}})
	}))
	defer upstream.Close()
	a.Cfg.EntranceURL = upstream.URL
	response := request(a, "POST", "/api/auth/verify-otp", a.Cfg.ServiceKeys["pos"], `{"processId":"process","otp":"1234"}`)
	var body map[string]any
	json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 200 || calls != 1 || body["success"] != false || body["nextStep"] != "kaspi_id" || a.session != nil || a.flows["process"].Finished {
		t.Fatal("OTP route incorrectly completed a login requiring photo verification")
	}
	if a.flows["process"].Step != "KaspiIdTakePhoto" || strings.Contains(response.Body.String(), "private-verification") {
		t.Fatal("photo challenge lost its step or exposed verification data")
	}
}
