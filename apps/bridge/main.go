package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("bad configuration", "error", err)
		os.Exit(1)
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
	slog.SetDefault(log)

	db := NewSupabaseClient(cfg.SupabaseURL, cfg.SupabaseKey)
	auth := NewAdminAuth(cfg.SupabaseURL, cfg.SupabaseKey, strings.HasPrefix(cfg.PublicBaseURL, "https://"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := waitForDatabase(ctx, db, 30*time.Second); err != nil {
		log.Error("supabase is not reachable", "error", err)
		os.Exit(1)
	}

	wa, err := newWhatsApp(cfg, db, waLog.Stdout("whatsapp", cfg.LogLevel, cfg.LogLevel == "debug"))
	if err != nil {
		log.Error("could not open the whatsapp store", "error", err)
		os.Exit(1)
	}
	defer wa.Close()

	server := NewServer(cfg, db, wa, auth, log)

	// Pairing needs network access to WhatsApp, so a failure here must not stop
	// the shop from serving orders.
	//
	// Start owns the connection once it succeeds: WhatsMeow reconnects on its
	// own and reports drops through events, so calling it again on a timer would
	// replace a perfectly healthy session and earn a StreamReplaced from the
	// server. Retry only when the initial dial actually failed.
	waCtx, cancelWA := context.WithCancel(ctx)
	defer cancelWA()
	go func() {
		backoff := 5 * time.Second
		for attempt := 1; ; attempt++ {
			err := wa.Start(waCtx)
			if err == nil {
				log.Info("whatsapp client started", "state", wa.state().Status)
				return
			}
			if waCtx.Err() != nil {
				return
			}

			log.Warn("whatsapp start failed", "attempt", attempt, "retry_in", backoff, "error", err)
			wa.setState("error", "", "", err)

			select {
			case <-waCtx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 2*time.Minute {
				backoff *= 2
			}
		}
	}()

	go server.runJobWorker(ctx)
	go server.runRetentionSweep(ctx)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Info("bridge listening", "addr", cfg.Addr, "public_url", cfg.PublicBaseURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server stopped", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "error", err)
	}
}

func waitForDatabase(ctx context.Context, db *Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var products []Product
		err := db.Select(probeCtx, "products", nil, &products)
		cancel()

		if err == nil {
			return nil
		}
		var apiErr *apiError
		if errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden) {
			return errors.New("the service key was rejected: check SUPABASE_SERVICE_KEY")
		}

		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return lastErr
}

func logLevel(raw string) slog.Level {
	switch raw {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
