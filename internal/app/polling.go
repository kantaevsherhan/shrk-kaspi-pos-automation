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
	for _, entry := range a.activePayments() {
		if entry.ExpireDate != "" {
			if expiry, err := parseKaspiTime(entry.ExpireDate); err == nil && time.Now().After(expiry) {
				a.finishPayment(entry, "payment.expired", map[string]any{"Status": "Expired", "StatusDesc": "Время оплаты истекло"})
				continue
			}
		}
		session, version := a.sessionSnapshot()
		if session == nil || time.Now().Before(entry.NextCheck) {
			continue
		}
		if entry.ProfileID != "" && entry.ProfileID != session.ProfileID {
			// Keep the pending payment for when its own organization reconnects.
			// A different account cannot establish whether it was paid.
			continue
		}
		var result map[string]any
		var err error
		if entry.Type == "qr" {
			result, err = a.qrStatus(ctx, session, entry.ID)
		} else {
			result, err = a.invoiceDetails(ctx, session, entry.ID)
		}
		a.mu.RLock()
		currentVersion := a.sessionVersion
		a.mu.RUnlock()
		if currentVersion != version {
			continue
		}
		if err != nil {
			log.Printf("payment status query failed for %s: %v", entry.ID, err)
		}
		data := obj(result["Data"])
		status := ""
		if kaspiOK(result) {
			status = stringValue(data["Status"])
		}
		a.mu.Lock()
		current := a.tracked[entry.ID]
		if current == nil {
			a.mu.Unlock()
			continue
		}
		if status == "" {
			// A network/auth failure does not establish a payment result. Retain
			// pending IDs across outages/restarts and resume details checks later.
			current.RetryCount = min(current.RetryCount+1, 7)
			delay := min(3*time.Second*time.Duration(1<<uint(current.RetryCount-1)), 2*time.Minute)
			current.NextCheck = time.Now().Add(delay)
			a.mu.Unlock()
			_ = a.saveTracked()
			continue
		}
		current.RetryCount = 0
		current.NextCheck = time.Time{}
		current.Status = status
		a.mu.Unlock()
		_ = a.saveTracked()
		if event := paymentEvent(entry.Type, status); event != "" {
			a.finishPayment(entry, event, data)
		}
	}
}
func (a *App) finishPayment(p *TrackedPayment, event string, data map[string]any) {
	payload := map[string]any{"event": event, "paymentId": p.ID, "type": p.Type, "status": stringValue(data["Status"]), "statusDesc": stringValue(data["StatusDesc"]), "amount": p.Amount, "qrToken": p.QRToken, "receiptUrl": p.ReceiptURL, "orderNumber": p.OrderNumber, "data": data, "timestamp": time.Now().UTC().Format(time.RFC3339)}
	if p.Amount == 0 {
		payload["amount"] = numberValue(data["Amount"])
	}
	if p.ReceiptURL == "" {
		payload["receiptUrl"] = stringValue(data["ReceiptUrl"])
	}
	if p.OrderNumber == "" {
		payload["orderNumber"] = stringValue(data["OrderNumber"])
	}
	hooks := a.webhooks(event)
	a.mu.Lock()
	if a.tracked[p.ID] == nil {
		a.mu.Unlock()
		return
	}
	// Persist deliveries before forgetting the payment. A restart may repeat a
	// delivery, but must not discard an undelivered event.
	for _, hook := range hooks {
		a.queueDeliveryLocked(hook, payload, 1, time.Now())
	}
	a.mu.Unlock()
	if len(hooks) > 0 {
		if err := a.saveRetries(); err != nil {
			log.Printf("could not persist payment webhook queue")
			return
		}
	}
	a.mu.Lock()
	delete(a.tracked, p.ID)
	a.mu.Unlock()
	_ = a.saveTracked()
	for _, hook := range hooks {
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
	a.mu.Lock()
	a.queueDeliveryLocked(hook, payload, attempt, time.Now())
	a.mu.Unlock()
	if err := a.saveRetries(); err != nil {
		log.Printf("could not persist webhook before delivery")
		return
	}
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
		a.removeDelivery(hook, payload)
		return
	}
	// Transport errors can include credentials embedded in the configured URL.
	log.Printf("webhook delivery failed (attempt %d)", attempt)
	if attempt >= 3 {
		a.removeDelivery(hook, payload)
		return
	}
	delay := 5 * time.Second
	if attempt > 1 {
		delay = 30 * time.Second
	}
	a.mu.Lock()
	a.queueDeliveryLocked(hook, payload, attempt+1, time.Now().Add(delay))
	a.mu.Unlock()
	_ = a.saveRetries()
}
func (a *App) processRetries(ctx context.Context) {
	a.mu.Lock()
	due := []webhookRetry{}
	for _, r := range a.retries {
		if !time.Now().Before(r.ExecuteAfter) {
			due = append(due, r)
		}
	}
	a.mu.Unlock()
	for _, r := range due {
		a.deliverWebhook(ctx, r.Hook, r.Payload, r.Attempt)
	}
}

func sameDelivery(r webhookRetry, hook Webhook, payload map[string]any) bool {
	return r.Hook.URL == hook.URL && stringValue(r.Payload["paymentId"]) == stringValue(payload["paymentId"]) && stringValue(r.Payload["event"]) == stringValue(payload["event"])
}
func (a *App) queueDeliveryLocked(hook Webhook, payload map[string]any, attempt int, at time.Time) {
	entry := webhookRetry{Hook: hook, Payload: payload, Attempt: attempt, ExecuteAfter: at}
	for i, current := range a.retries {
		if sameDelivery(current, hook, payload) {
			a.retries[i] = entry
			return
		}
	}
	a.retries = append(a.retries, entry)
}
func (a *App) removeDelivery(hook Webhook, payload map[string]any) {
	a.mu.Lock()
	pending := a.retries[:0]
	for _, current := range a.retries {
		if !sameDelivery(current, hook, payload) {
			pending = append(pending, current)
		}
	}
	a.retries = pending
	a.mu.Unlock()
	_ = a.saveRetries()
}
func parseKaspiTime(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-0700"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("invalid Kaspi timestamp")
}
