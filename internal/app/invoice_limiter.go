package app

import (
	"context"
	"time"
)

// beginInvoiceSend serializes invoice submissions from every API caller. The
// interval begins after the previous submission finishes, including failed
// submissions. Waiting happens before signing, so the MAC is generated fresh.
// No slots are reserved for callers that cancel while waiting.
func (a *App) beginInvoiceSend(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	interval := a.Cfg.InvoiceSendInterval
	if interval <= 0 {
		return func() {}, nil
	}
	a.invoiceGateMu.Lock()
	if a.invoiceGate == nil {
		a.invoiceGate = make(chan struct{}, 1)
	}
	gate := a.invoiceGate
	a.invoiceGateMu.Unlock()
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if wait := time.Until(a.invoiceNextSend); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			<-gate
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		<-gate
		return nil, err
	}
	return func() {
		a.invoiceNextSend = time.Now().Add(interval)
		<-gate
	}, nil
}
