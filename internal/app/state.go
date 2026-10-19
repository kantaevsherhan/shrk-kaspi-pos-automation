package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type KaspiSession struct {
	TokenSN        string `json:"tokenSN"`
	Secret         []byte `json:"secret"`
	ProfileID      string `json:"profileId"`
	OrgName        string `json:"orgName"`
	Phone          string `json:"phone"`
	OrganizationID string `json:"organizationId"`
}
type authFlow struct {
	ProcessID, UserToken, Phone string
	Created                     time.Time
}
type TrackedPayment struct {
	ID          string    `json:"paymentId"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	Amount      float64   `json:"amount"`
	QRToken     string    `json:"qrToken,omitempty"`
	ReceiptURL  string    `json:"receiptUrl,omitempty"`
	OrderNumber string    `json:"orderNumber,omitempty"`
	ExpireDate  string    `json:"expireDate,omitempty"`
	Created     time.Time `json:"createdAt"`
	RetryCount  int       `json:"retryCount"`
}
type Webhook struct {
	URL    string   `json:"url"`
	Secret string   `json:"secret"`
	Events []string `json:"events"`
}
type webhookRetry struct {
	Hook         Webhook        `json:"hook"`
	Payload      map[string]any `json:"payload"`
	Attempt      int            `json:"attempt"`
	ExecuteAfter time.Time      `json:"executeAfter"`
}

type App struct {
	Cfg     *Config
	client  *http.Client
	mu      sync.RWMutex
	session *KaspiSession
	flows   map[string]*authFlow
	tracked map[string]*TrackedPayment
	retries []webhookRetry
}

func New(cfg *Config) (*App, error) {
	a := &App{Cfg: cfg, client: &http.Client{Timeout: 15 * time.Second}, flows: map[string]*authFlow{}, tracked: map[string]*TrackedPayment{}}
	if err := a.loadSession(); err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(filepath.Join(cfg.StateDir, "tracked-payments.json")); err == nil {
		if err = json.Unmarshal(b, &a.tracked); err != nil {
			a.tracked, err = readLegacyPayments(b)
			if err != nil {
				return nil, err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if b, err := os.ReadFile(filepath.Join(cfg.StateDir, "webhook-retries.json")); err == nil {
		if err = json.Unmarshal(b, &a.retries); err != nil {
			a.retries, err = readLegacyRetries(b)
			if err != nil {
				return nil, err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return a, nil
}

func readLegacyPayments(b []byte) (map[string]*TrackedPayment, error) {
	var old map[string]struct {
		PaymentID  string `json:"paymentId"`
		Type       string `json:"type"`
		Status     string `json:"status"`
		CreatedAt  int64  `json:"createdAt"`
		RetryCount int    `json:"retryCount"`
		Meta       struct {
			Amount      float64 `json:"amount"`
			QRToken     string  `json:"qrToken"`
			ReceiptURL  string  `json:"receiptUrl"`
			OrderNumber string  `json:"orderNumber"`
			ExpireDate  string  `json:"expireDate"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(b, &old); err != nil {
		return nil, err
	}
	converted := map[string]*TrackedPayment{}
	for id, p := range old {
		if id == "" {
			continue
		}
		converted[id] = &TrackedPayment{ID: id, Type: p.Type, Status: p.Status, Amount: p.Meta.Amount, QRToken: p.Meta.QRToken, ReceiptURL: p.Meta.ReceiptURL, OrderNumber: p.Meta.OrderNumber, ExpireDate: p.Meta.ExpireDate, Created: time.UnixMilli(p.CreatedAt), RetryCount: p.RetryCount}
	}
	return converted, nil
}
func readLegacyRetries(b []byte) ([]webhookRetry, error) {
	var old []struct {
		Hook         Webhook        `json:"hook"`
		Payload      map[string]any `json:"payload"`
		Attempt      int            `json:"attempt"`
		ExecuteAfter int64          `json:"executeAfter"`
	}
	if err := json.Unmarshal(b, &old); err != nil {
		return nil, err
	}
	converted := make([]webhookRetry, 0, len(old))
	for _, r := range old {
		converted = append(converted, webhookRetry{Hook: r.Hook, Payload: r.Payload, Attempt: r.Attempt, ExecuteAfter: time.UnixMilli(r.ExecuteAfter)})
	}
	return converted, nil
}

func (a *App) sessionPath() string { return filepath.Join(a.Cfg.StateDir, "session.enc") }
func (a *App) loadSession() error {
	b, err := os.ReadFile(a.sessionPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := decrypt(a.Cfg.TokenKey, string(b))
	if err != nil {
		return err
	}
	var s KaspiSession
	if err = json.Unmarshal(plain, &s); err != nil {
		return err
	}
	a.session = &s
	return nil
}
func (a *App) saveSession(s *KaspiSession) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	encrypted, err := encrypt(a.Cfg.TokenKey, b)
	if err != nil {
		return err
	}
	path := a.sessionPath()
	if err = os.WriteFile(path+".tmp", []byte(encrypted), 0600); err != nil {
		return err
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		return err
	}
	a.mu.Lock()
	a.session = s
	a.mu.Unlock()
	return nil
}
func (a *App) clearSession() error {
	a.mu.Lock()
	a.session = nil
	a.mu.Unlock()
	err := os.Remove(a.sessionPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (a *App) currentSession() *KaspiSession {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.session == nil {
		return nil
	}
	copy := *a.session
	copy.Secret = append([]byte(nil), a.session.Secret...)
	return &copy
}
func (a *App) saveTracked() error {
	a.mu.RLock()
	b, err := json.MarshalIndent(a.tracked, "", "  ")
	a.mu.RUnlock()
	if err != nil {
		return err
	}
	return writeBytes(filepath.Join(a.Cfg.StateDir, "tracked-payments.json"), b)
}
func (a *App) saveRetries() error {
	a.mu.RLock()
	b, err := json.MarshalIndent(a.retries, "", "  ")
	a.mu.RUnlock()
	if err != nil {
		return err
	}
	return writeBytes(filepath.Join(a.Cfg.StateDir, "webhook-retries.json"), b)
}
func writeBytes(path string, b []byte) error {
	if err := os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (a *App) track(p *TrackedPayment) {
	a.mu.Lock()
	a.tracked[p.ID] = p
	a.mu.Unlock()
	_ = a.saveTracked()
}
func (a *App) activePayments() []*TrackedPayment {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*TrackedPayment, 0, len(a.tracked))
	for _, p := range a.tracked {
		copy := *p
		out = append(out, &copy)
	}
	return out
}
