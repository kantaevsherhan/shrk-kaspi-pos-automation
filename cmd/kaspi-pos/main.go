package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kantaevsherhan/shrk-kaspi-pos-automation/internal/app"
)

func main() {
	cfg, err := app.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	service, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service.StartPolling(ctx)
	service.StartSessionMonitor(ctx)
	server := &http.Server{Addr: cfg.Host + ":" + cfg.Port, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Kaspi POS listening on %s:%s", cfg.Host, cfg.Port)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
