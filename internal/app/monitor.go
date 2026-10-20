package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type monitorState struct {
	EverConnected      bool      `json:"everConnected"`
	Status             string    `json:"status"`
	LastNotifiedStatus string    `json:"lastNotifiedStatus"`
	LastChecked        time.Time `json:"lastChecked"`
	Reason             string    `json:"reason,omitempty"`
}

func (a *App) monitorPath() string { return filepath.Join(a.Cfg.StateDir, "session-monitor.json") }
func (a *App) loadMonitor() error {
	b, err := os.ReadFile(a.monitorPath())
	if err == nil {
		if err := json.Unmarshal(b, &a.monitor); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if a.session != nil {
		a.monitor.EverConnected = true
	}
	return nil
}
func (a *App) saveMonitorLocked() error { return writeJSON(a.monitorPath(), a.monitor) }

// StartSessionMonitor checks Kaspi on the server, including when no browser is open.
func (a *App) StartSessionMonitor(ctx context.Context) {
	interval := a.Cfg.SessionCheckInterval
	if interval == 0 {
		interval = 2 * time.Minute
	}
	go func() {
		a.monitorCycle(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.monitorCycle(ctx)
			}
		}
	}()
}

func (a *App) monitorCycle(ctx context.Context) {
	a.monitorCheckMu.Lock()
	defer a.monitorCheckMu.Unlock()
	s := a.currentSession()
	status, reason := "connected", ""
	if s == nil {
		a.mu.RLock()
		seen := a.monitor.EverConnected
		a.mu.RUnlock()
		if seen {
			status, reason = "disconnected", "session_missing"
		} else {
			status = "not_connected"
		}
	} else {
		checkCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		active, _, err := a.checkSession(checkCtx, s)
		cancel()
		if err != nil {
			a.monitorFailures++
			if a.monitorFailures < 3 {
				a.mu.Lock()
				a.monitor.LastChecked = time.Now().UTC()
				_ = a.saveMonitorLocked()
				a.mu.Unlock()
				return
			}
			status, reason = "disconnected", "check_failed"
		} else {
			a.monitorFailures = 0
			if !active {
				status, reason = "disconnected", "kaspi_rejected"
			}
		}
	}
	a.updateMonitor(ctx, status, reason)
}

func (a *App) updateMonitor(ctx context.Context, status, reason string) {
	a.mu.Lock()
	a.monitor.Status = status
	a.monitor.Reason = reason
	a.monitor.LastChecked = time.Now().UTC()
	if status == "connected" && a.monitor.LastNotifiedStatus == "" {
		a.monitor.LastNotifiedStatus = "connected"
	}
	shouldNotify := a.Cfg.TelegramBotToken != "" && status != "not_connected" && a.monitor.LastNotifiedStatus != status
	if err := a.saveMonitorLocked(); err != nil {
		log.Printf("session monitor state save failed: %v", err)
	}
	a.mu.Unlock()
	if !shouldNotify {
		return
	}
	message := "✅ Kaspi POS: подключение к Kaspi восстановлено."
	if status == "disconnected" {
		switch reason {
		case "session_missing":
			message = "⚠️ Kaspi POS: сессия Kaspi отсутствует. Подключите аккаунт в панели."
		case "kaspi_rejected":
			message = "⚠️ Kaspi POS: Kaspi отклонил сессию. Войдите повторно по SMS в панели."
		default:
			message = "⚠️ Kaspi POS: три проверки подряд не смогли связаться с Kaspi. Проверьте подключение."
		}
	}
	if err := a.sendTelegram(ctx, message); err != nil {
		log.Printf("Telegram notification failed: %v", err)
		return
	}
	a.mu.Lock()
	if a.monitor.Status == status {
		a.monitor.LastNotifiedStatus = status
		if err := a.saveMonitorLocked(); err != nil {
			log.Printf("session monitor state save failed: %v", err)
		}
	}
	a.mu.Unlock()
}

func (a *App) sendTelegram(ctx context.Context, message string) error {
	if a.Cfg.TelegramBotToken == "" || a.Cfg.TelegramChatID == "" {
		return errors.New("Telegram is not configured")
	}
	base := a.telegramBaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	data, err := json.Marshal(map[string]string{"chat_id": a.Cfg.TelegramChatID, "text": message})
	if err != nil {
		return errors.New("Telegram request encoding failed")
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sendCtx, "POST", base+"/bot"+a.Cfg.TelegramBotToken+"/sendMessage", bytes.NewReader(data))
	if err != nil {
		return errors.New("Telegram request creation failed")
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Telegram request failed")
	}
	defer resp.Body.Close()
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&result); err != nil {
		return fmt.Errorf("Telegram returned invalid response (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		return fmt.Errorf("Telegram rejected notification (HTTP %d)", resp.StatusCode)
	}
	return nil
}
