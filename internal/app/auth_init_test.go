package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEntranceInitDoesNotOverwriteLaunchParameters(t *testing.T) {
	a := testApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/entrance/step" {
			t.Error("unexpected entrance request")
		}
		body, _ := io.ReadAll(r.Body)
		// Match Kaspi's case-insensitive JSON binding. The old Data + data
		// payload overwrote this map with {}, causing a real upstream HTTP 400.
		var bound struct {
			Data    map[string]string `json:"data"`
			ActType string            `json:"actType"`
		}
		if err := json.Unmarshal(body, &bound); err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(body, &fields)
		dataFields := 0
		for name := range fields {
			if strings.EqualFold(name, "data") {
				dataFields++
			}
		}
		if dataFields != 1 || bound.Data["auth"] != "2" || bound.Data["pc"] != "KPEntrance" || bound.Data["deviceId"] != a.Cfg.Device.DeviceID || bound.Data["installId"] != a.Cfg.Device.InstallID || bound.Data["appVersion"] != a.Cfg.App.Version || bound.Data["appBuild"] != a.Cfg.App.Build || bound.ActType != "Success" {
			t.Error("launch parameters erased or ambiguous")
			w.WriteHeader(400)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "user_token", Value: "synthetic-cookie"})
		io.WriteString(w, `{"meta":{"pId":"synthetic-process","sn":"EnterPhoneNumber"},"type":"View","view":{"code":"KPUniversalEnterPhoneNumber"}}`)
	}))
	defer upstream.Close()
	a.Cfg.EntranceURL = upstream.URL
	response := request(a, "POST", "/api/auth/init", a.Cfg.ServiceKeys["pos"], `{}`)
	if response.Code != 200 {
		t.Fatalf("init failed: HTTP %d", response.Code)
	}
	var result map[string]any
	json.Unmarshal(response.Body.Bytes(), &result)
	if result["success"] != true || result["view"] != "KPUniversalEnterPhoneNumber" || result["processId"] != "synthetic-process" {
		t.Fatal("real entrance view contract was not accepted")
	}
	flow, err := a.getFlow("synthetic-process")
	if err != nil || flow.UserToken != "synthetic-cookie" {
		t.Fatal("entrance session not retained for the phone step")
	}
}
