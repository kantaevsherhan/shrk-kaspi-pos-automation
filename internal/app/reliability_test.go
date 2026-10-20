package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentLoginAttemptsRespectLimit(t *testing.T) {
	a := testApp(t)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Go(func() {
			if a.beginLoginAttempt("127.0.0.1") {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 5 {
		t.Fatalf("allowed %d concurrent guesses instead of five", allowed.Load())
	}
}

func TestConcurrentPhoneStepsAreSerialized(t *testing.T) {
	a := testApp(t)
	a.flows["process"] = &authFlow{ProcessID: "process", Created: time.Now()}
	var active, overlap atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if active.Add(1) > 1 {
			overlap.Add(1)
		}
		defer active.Add(-1)
		time.Sleep(10 * time.Millisecond)
		http.SetCookie(w, &http.Cookie{Name: "user_token", Value: "updated-token"})
		io.WriteString(w, `{"view":{"code":"EnterOtp"}}`)
	}))
	defer upstream.Close()
	a.Cfg.EntranceURL = upstream.URL
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Go(func() {
			if _, err := a.sendPhone(context.Background(), "process", "77001234567"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if overlap.Load() != 0 {
		t.Fatal("concurrent auth steps can overwrite cookies and phone state")
	}
}

func TestConcurrentPendingWritesSurviveRestart(t *testing.T) {
	a := testApp(t)
	var wg sync.WaitGroup
	for i := 1; i <= 50; i++ {
		wg.Go(func() {
			id := fmt.Sprint(i)
			a.mu.Lock()
			a.tracked[id] = &TrackedPayment{ID: id, Type: "qr", Amount: 100, Created: time.Now()}
			a.mu.Unlock()
			if err := a.saveTracked(); err != nil {
				t.Errorf("concurrent write failed: %v", err)
			}
		})
	}
	wg.Wait()
	restored, err := New(a.Cfg)
	if err != nil || len(restored.activePayments()) != 50 {
		t.Fatalf("lost concurrent payments: %v", err)
	}
}

func TestConcurrentSessionWritesStayConsistent(t *testing.T) {
	a := testApp(t)
	a.Cfg.TokenKey = bytes.Repeat([]byte{1}, 32)
	var wg sync.WaitGroup
	for i := 1; i <= 12; i++ {
		wg.Go(func() {
			if err := a.saveSession(&KaspiSession{TokenSN: fmt.Sprintf("token-%d", i), Secret: bytes.Repeat([]byte{byte(i)}, 32), ProfileID: "42"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	restored, err := New(a.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.currentSession(); got == nil || got.TokenSN != a.currentSession().TokenSN {
		t.Fatal("session on disk differs from memory")
	}
}

func TestWebhookRemainsDurableWhileRequestIsInFlight(t *testing.T) {
	a := testApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		restored, err := New(a.Cfg)
		if err != nil || len(restored.retries) != 1 {
			t.Errorf("in-flight delivery disappeared from disk: %v", err)
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	hook := Webhook{URL: upstream.URL, Secret: "test-secret"}
	payload := map[string]any{"paymentId": "123", "event": "payment.success"}
	a.deliverWebhook(context.Background(), hook, payload, 1)
	if len(a.retries) != 0 {
		t.Fatal("successful webhook still pending")
	}
	// A due retry must remain durable until its HTTP response is confirmed too.
	a.mu.Lock()
	a.queueDeliveryLocked(hook, payload, 2, time.Now().Add(-time.Second))
	a.mu.Unlock()
	if err := a.saveRetries(); err != nil {
		t.Fatal(err)
	}
	a.processRetries(context.Background())
	restored, err := New(a.Cfg)
	if err != nil || len(restored.retries) != 0 {
		t.Fatalf("retry not acknowledged: %v", err)
	}
}

func TestPollingExpiryProfilesAndUpstreamErrors(t *testing.T) {
	t.Run("Kaspi timezone expiry without a session", func(t *testing.T) {
		a := testApp(t)
		a.track(&TrackedPayment{ID: "123", Type: "qr", ExpireDate: "2020-01-01T00:00:00.000+0500"})
		a.pollOnce(context.Background())
		if len(a.activePayments()) != 0 {
			t.Fatal("expired QR retained without session")
		}
	})
	t.Run("another organization", func(t *testing.T) {
		a := testApp(t)
		a.session = &KaspiSession{ProfileID: "new-profile"}
		a.track(&TrackedPayment{ID: "123", Type: "invoice", ProfileID: "old-profile"})
		a.pollOnce(context.Background())
		if len(a.activePayments()) != 0 {
			t.Fatal("payment from another account was still polled")
		}
	})
	t.Run("error response cannot finalize payment", func(t *testing.T) {
		a := testApp(t)
		a.session = &KaspiSession{ProfileID: "profile"}
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"StatusCode":-1,"Data":{"Status":"Processed"}}`)
		}))
		defer upstream.Close()
		a.Cfg.QRPayURL = upstream.URL
		a.track(&TrackedPayment{ID: "123", Type: "qr", ProfileID: "profile"})
		a.pollOnce(context.Background())
		if len(a.activePayments()) != 1 {
			t.Fatal("failed Kaspi response reported successful payment")
		}
	})
}

func TestFinalPaymentQueuesWebhookBeforeRemovingTracking(t *testing.T) {
	a := testApp(t)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer hook.Close()
	config, _ := json.Marshal([]Webhook{{URL: hook.URL, Secret: "test", Events: []string{"payment.success"}}})
	if err := os.WriteFile(filepath.Join(a.Cfg.StateDir, "webhooks.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	a.track(&TrackedPayment{ID: "123", Type: "invoice", Amount: 100})
	a.finishPayment(a.activePayments()[0], "payment.success", map[string]any{"Status": "Processed"})
	restored, err := New(a.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.activePayments()) != 0 || len(restored.retries) != 1 || restored.retries[0].Attempt != 2 {
		t.Fatal("final payment or webhook state lost")
	}
}

func TestMonitorIgnoresResponseFromReplacedSession(t *testing.T) {
	a := testApp(t)
	a.Cfg.TokenKey = bytes.Repeat([]byte{1}, 32)
	a.session = &KaspiSession{TokenSN: "old-token", Secret: bytes.Repeat([]byte{1}, 32), ProfileID: "old"}
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		io.WriteString(w, `{"StatusCode":-101001}`)
	}))
	defer upstream.Close()
	a.Cfg.QRPayURL = upstream.URL
	done := make(chan struct{})
	go func() { a.monitorCycle(context.Background()); close(done) }()
	<-started
	if err := a.saveSession(&KaspiSession{TokenSN: "new-token", Secret: bytes.Repeat([]byte{2}, 32), ProfileID: "new"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if a.monitor.Status == "disconnected" {
		t.Fatal("old session failure invalidated a newly connected session")
	}
}

func TestPollingDoesNotLosePaymentWhenSessionChangesDuringRequest(t *testing.T) {
	a := testApp(t)
	a.Cfg.TokenKey = bytes.Repeat([]byte{1}, 32)
	a.session = &KaspiSession{TokenSN: "old", ProfileID: "42"}
	a.track(&TrackedPayment{ID: "123", Type: "qr", ProfileID: "42"})
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		io.WriteString(w, `{"StatusCode":-101001}`)
	}))
	defer upstream.Close()
	a.Cfg.QRPayURL = upstream.URL
	done := make(chan struct{})
	go func() { a.pollOnce(context.Background()); close(done) }()
	<-started
	if err := a.saveSession(&KaspiSession{TokenSN: "new", ProfileID: "42"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if len(a.activePayments()) != 1 {
		t.Fatal("old session response discarded a payment after reconnection")
	}
}
