package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestSessionMonitorTelegramTransitions(t *testing.T) {
	a := testApp(t)
	a.Cfg.TelegramBotToken = "123:test-secret-token"
	a.Cfg.TelegramChatID = "456"
	var mu sync.Mutex
	mode := "connected"
	kaspi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		current := mode
		mu.Unlock()
		if r.URL.Path != "/v02/history/operations" {
			t.Errorf("unexpected Kaspi path: %s", r.URL.Path)
		}
		if current == "bad" {
			io.WriteString(w, "not json")
			return
		}
		if current == "rejected" {
			io.WriteString(w, `{"StatusCode":-101001}`)
			return
		}
		io.WriteString(w, `{"StatusCode":0,"Data":{}}`)
	}))
	defer kaspi.Close()
	a.Cfg.QRPayURL = kaspi.URL
	a.session = &KaspiSession{TokenSN: "token", Secret: []byte("01234567890123456789012345678901"), ProfileID: "profile"}
	var messages []string
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:test-secret-token/sendMessage" || r.Method != "POST" {
			t.Errorf("unexpected Telegram request: %s", r.URL.Path)
		}
		var body struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ChatID != "456" {
			t.Error("wrong Telegram chat")
		}
		mu.Lock()
		messages = append(messages, body.Text)
		mu.Unlock()
		io.WriteString(w, `{"ok":true}`)
	}))
	defer telegram.Close()
	a.telegramBaseURL = telegram.URL
	a.monitorCycle(context.Background())
	if a.monitor.Status != "connected" || len(messages) != 0 {
		t.Fatalf("initial check: %+v, %v", a.monitor, messages)
	}
	mu.Lock()
	mode = "rejected"
	mu.Unlock()
	a.monitorCycle(context.Background())
	a.monitorCycle(context.Background())
	if a.monitor.Status != "disconnected" || len(messages) != 1 || !strings.Contains(messages[0], "отклонил") {
		t.Fatalf("disconnect: %+v, %v", a.monitor, messages)
	}
	mu.Lock()
	mode = "connected"
	mu.Unlock()
	a.monitorCycle(context.Background())
	if a.monitor.Status != "connected" || len(messages) != 2 || !strings.Contains(messages[1], "восстановлено") {
		t.Fatalf("recovery: %+v, %v", a.monitor, messages)
	}
	if _, err := os.Stat(a.monitorPath()); err != nil {
		t.Fatal(err)
	}
	previous := a.monitor
	a.monitor = monitorState{}
	if err := a.loadMonitor(); err != nil || a.monitor.LastNotifiedStatus != previous.LastNotifiedStatus {
		t.Fatal("monitor state did not survive restart", err)
	}
	a.monitorCycle(context.Background())
	if len(messages) != 2 {
		t.Fatal("duplicate notification after restart")
	}
	login := httptest.NewRecorder()
	if err := a.newAdminSession(login, httptest.NewRequest("GET", "http://example.com/", nil)); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0].String()
	info := adminRequest(a, "GET", "/api/admin/monitor", "", "", cookie, false)
	if info.Code != 200 || strings.Contains(info.Body.String(), a.Cfg.TelegramBotToken) {
		t.Fatal("monitor endpoint exposed secrets or failed", info.Code)
	}
	if got := adminRequest(a, "POST", "/api/admin/notifications/test", "", "", cookie, true); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	if len(messages) != 3 || !strings.Contains(messages[2], "тестовое") {
		t.Fatal("test message not delivered")
	}
	if err := a.clearSession(); err != nil {
		t.Fatal(err)
	}
	a.monitorCycle(context.Background())
	if len(messages) != 4 || !strings.Contains(messages[3], "отсутствует") {
		t.Fatal(messages)
	}
}

func TestSessionMonitorWaitsForRepeatedCheckFailures(t *testing.T) {
	a := testApp(t)
	a.session = &KaspiSession{TokenSN: "token", Secret: []byte("01234567890123456789012345678901")}
	mode := "connected"
	kaspi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "bad" {
			io.WriteString(w, "bad")
			return
		}
		io.WriteString(w, `{"StatusCode":0}`)
	}))
	defer kaspi.Close()
	a.Cfg.QRPayURL = kaspi.URL
	a.monitorCycle(context.Background())
	mode = "bad"
	a.monitorCycle(context.Background())
	a.monitorCycle(context.Background())
	if a.monitor.Status != "connected" {
		t.Fatal("alerted on transient failure")
	}
	a.monitorCycle(context.Background())
	if a.monitor.Status != "disconnected" || a.monitor.Reason != "check_failed" {
		t.Fatalf("third failure ignored: %+v", a.monitor)
	}
}

func TestTelegramErrorDoesNotExposeToken(t *testing.T) {
	a := testApp(t)
	a.Cfg.TelegramBotToken = "123:private-token"
	a.Cfg.TelegramChatID = "456"
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, `{"ok":false,"description":"private-token"}`)
	}))
	defer telegram.Close()
	a.telegramBaseURL = telegram.URL
	err := a.sendTelegram(context.Background(), "test")
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatal("token exposed in Telegram error")
	}
}
