package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

var qrFinal = map[string]string{"Processed": "payment.success", "CancelledByUser": "payment.failed", "NotConfirmedByUser": "payment.failed", "CancelledByExternalSource": "payment.failed", "ProcessingFailed": "payment.failed", "Rejected": "payment.failed", "InsufficientFunds": "payment.failed", "InsufficientFundsError": "payment.failed", "Error": "payment.failed", "IrisSrcBlockCode1": "payment.failed", "IrisSrcBlockCode3": "payment.failed", "IrisSrcBlockCode9": "payment.failed", "IrisDestBlockCode3": "payment.failed", "IrisDestBlockCode5": "payment.failed", "IrisDestBlockCode7": "payment.failed", "IrisDestBlockCode10": "payment.failed", "QrTokenDiscarded": "payment.expired", "Expired": "payment.expired"}
var invoiceFinal = map[string]string{"Processed": "payment.success", "RemotePaymentCanceled": "payment.failed", "RemotePaymentRejected": "payment.failed", "Expired": "payment.expired"}

func paymentEvent(kind, status string) string {
	if kind == "qr" {
		return qrFinal[status]
	}
	return invoiceFinal[status]
}
func (a *App) StartPolling(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.pollOnce(ctx)
				a.processRetries(ctx)
			}
		}
	}()
}
func (a *App) pollOnce(ctx context.Context) {
	session := a.currentSession()
	if session == nil {
		return
	}
	for _, entry := range a.activePayments() {
		if entry.ExpireDate != "" {
			if expiry, err := time.Parse(time.RFC3339, entry.ExpireDate); err == nil && time.Now().After(expiry) {
				a.finishPayment(entry, "payment.expired", map[string]any{"Status": "Expired", "StatusDesc": "Время оплаты истекло"})
				continue
			}
		}
		var result map[string]any
		var err error
		if entry.Type == "qr" {
			result, err = a.qrStatus(ctx, session, entry.ID)
		} else {
			result, err = a.invoiceDetails(ctx, session, entry.ID)
		}
		if err != nil {
			log.Printf("payment status query failed for %s: %v", entry.ID, err)
		}
		if numberValue(result["StatusCode"]) == -101001 {
			a.finishPayment(entry, "payment.lost", map[string]any{"Status": "SessionExpired"})
			continue
		}
		data := obj(result["Data"])
		status := stringValue(data["Status"])
		a.mu.Lock()
		current := a.tracked[entry.ID]
		if current == nil {
			a.mu.Unlock()
			continue
		}
		if status == "" {
			current.RetryCount++
			retries := current.RetryCount
			a.mu.Unlock()
			if retries > 10 {
				a.finishPayment(entry, "payment.lost", map[string]any{"Status": "PollingFailed"})
			} else {
				_ = a.saveTracked()
			}
			continue
		}
		current.RetryCount = 0
		current.Status = status
		a.mu.Unlock()
		_ = a.saveTracked()
		if event := paymentEvent(entry.Type, status); event != "" {
			a.finishPayment(entry, event, data)
		}
	}
}
func (a *App) finishPayment(p *TrackedPayment, event string, data map[string]any) {
	a.mu.Lock()
	if a.tracked[p.ID] == nil {
		a.mu.Unlock()
		return
	}
	delete(a.tracked, p.ID)
	a.mu.Unlock()
	_ = a.saveTracked()
	payload := map[string]any{"event": event, "paymentId": p.ID, "type": p.Type, "status": stringValue(data["Status"]), "statusDesc": stringValue(data["StatusDesc"]), "amount": p.Amount, "qrToken": p.QRToken, "receiptUrl": p.ReceiptURL, "orderNumber": p.OrderNumber, "data": data, "timestamp": time.Now().UTC().Format(time.RFC3339)}
	for _, hook := range a.webhooks(event) {
		a.deliverWebhook(context.Background(), hook, payload, 1)
	}
}
func (a *App) webhooks(event string) []Webhook {
	path := os.Getenv("WEBHOOKS_FILE")
	if path == "" {
		path = filepath.Join(a.Cfg.StateDir, "webhooks.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("webhook config read failed: %v", err)
		}
		return nil
	}
	var configured []Webhook
	if err = json.Unmarshal(raw, &configured); err != nil {
		log.Printf("webhook config invalid: %v", err)
		return nil
	}
	out := []Webhook{}
	for _, hook := range configured {
		if hook.URL == "" || hook.Secret == "" {
			continue
		}
		for _, e := range hook.Events {
			if e == event {
				out = append(out, hook)
				break
			}
		}
	}
	return out
}
func (a *App) deliverWebhook(ctx context.Context, hook Webhook, payload map[string]any, attempt int) {
	body, _ := json.Marshal(payload)
	mac := hmac.New(sha256.New, []byte(hook.Secret))
	mac.Write(body)
	deliveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(deliveryCtx, "POST", hook.URL, bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		var resp *http.Response
		resp, err = a.client.Do(req)
		if resp != nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				err = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		}
	}
	if err == nil {
		return
	}
	log.Printf("webhook delivery failed (attempt %d): %v", attempt, err)
	if attempt >= 3 {
		return
	}
	delay := 5 * time.Second
	if attempt > 1 {
		delay = 30 * time.Second
	}
	a.mu.Lock()
	a.retries = append(a.retries, webhookRetry{Hook: hook, Payload: payload, Attempt: attempt + 1, ExecuteAfter: time.Now().Add(delay)})
	a.mu.Unlock()
	_ = a.saveRetries()
}
func (a *App) processRetries(ctx context.Context) {
	a.mu.Lock()
	due := []webhookRetry{}
	pending := []webhookRetry{}
	for _, r := range a.retries {
		if !time.Now().Before(r.ExecuteAfter) {
			due = append(due, r)
		} else {
			pending = append(pending, r)
		}
	}
	a.retries = pending
	a.mu.Unlock()
	if len(due) > 0 {
		_ = a.saveRetries()
	}
	for _, r := range due {
		a.deliverWebhook(ctx, r.Hook, r.Payload, r.Attempt)
	}
}
