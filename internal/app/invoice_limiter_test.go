package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestInvoiceIntervalSharedByConcurrentServiceCallers(t *testing.T) {
	a := testApp(t)
	a.Cfg.InvoiceSendInterval = 30 * time.Millisecond
	a.Cfg.ServiceKeys["billing"] = "another-service-secret-of-at-least-32-characters"
	a.session = &KaspiSession{TokenSN: "synthetic", ProfileID: "42"}
	var mu sync.Mutex
	var arrivals []time.Time
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v01/remote/create" {
			t.Error("unexpected request path")
		}
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		// Failed submissions must also respect the next interval.
		io.WriteString(w, `{"StatusCode":-1}`)
	}))
	defer upstream.Close()
	a.Cfg.QRPayURL = upstream.URL
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		key := a.Cfg.ServiceKeys["pos"]
		if i%2 == 0 {
			key = a.Cfg.ServiceKeys["billing"]
		}
		wg.Go(func() {
			<-start
			got := request(a, "POST", "/api/invoice/create", key, `{"phoneNumber":"77001234567","amount":100}`)
			if got.Code != 200 {
				t.Errorf("invoice response: %d", got.Code)
			}
		})
	}
	close(start)
	wg.Wait()
	if len(arrivals) != 4 {
		t.Fatalf("expected four submissions, got %d", len(arrivals))
	}
	for i := 1; i < len(arrivals); i++ {
		if elapsed := arrivals[i].Sub(arrivals[i-1]); elapsed < a.Cfg.InvoiceSendInterval {
			t.Errorf("concurrent callers bypassed the interval: %s", elapsed)
		}
	}
}

func TestInvoiceWaitCancellationDoesNotSendOrReserveAnotherInterval(t *testing.T) {
	a := testApp(t)
	a.Cfg.InvoiceSendInterval = time.Second
	deadline := time.Now().Add(time.Minute)
	a.invoiceNextSend = deadline
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := a.createInvoice(ctx, &KaspiSession{}, "77001234567", 100, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled request attempted to submit: %v", err)
	}
	if a.invoiceNextSend != deadline || len(a.invoiceGate) != 0 {
		t.Fatal("canceled waiter consumed an interval or left the queue blocked")
	}
	// Also test cancellation while another sender owns the gate.
	a.invoiceGate <- struct{}{}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := a.beginInvoiceSend(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled caller waited for the active sender")
	}
	<-a.invoiceGate
}

func TestOnlyInvoiceSubmissionWaits(t *testing.T) {
	a := testApp(t)
	a.Cfg.InvoiceSendInterval = time.Minute
	session := &KaspiSession{TokenSN: "synthetic", ProfileID: "42"}
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v01/remote/create" {
			close(started)
			<-release
		}
		io.WriteString(w, `{"StatusCode":0,"Data":{}}`)
	}))
	defer upstream.Close()
	a.Cfg.QRPayURL = upstream.URL
	done := make(chan error, 1)
	go func() {
		_, err := a.createInvoice(context.Background(), session, "77001234567", 100, "")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("first invoice did not send immediately")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	checks := []func() error{
		func() error { _, err := a.clientInfo(ctx, session, "77001234567"); return err },
		func() error { _, err := a.invoiceHistory(ctx, session); return err },
		func() error { _, err := a.createQR(ctx, session, 100, 0, 0); return err },
		func() error { _, err := a.refund(ctx, session, "123", 100); return err },
	}
	for _, check := range checks {
		if err := check(); err != nil {
			t.Error("another endpoint was blocked by invoice submission", err)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
