package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr                  string
	PublicBaseURL         string
	SupabaseURL           string
	SupabaseKey           string
	WhatsAppDB            string
	WhatsAppDialect       string
	AutoReply             bool
	PollInterval          time.Duration
	SendGap               time.Duration
	WorkerName            string
	LogLevel              string
	RazorpayKeyID         string
	RazorpayKeySecret     string
	RazorpayWebhookSecret string
}

func loadConfig() (Config, error) {
	c := Config{
		Addr:                  env("BRIDGE_ADDR", ":8081"),
		PublicBaseURL:         strings.TrimRight(env("PUBLIC_BASE_URL", "http://localhost:8081"), "/"),
		SupabaseURL:           strings.TrimRight(os.Getenv("SUPABASE_URL"), "/"),
		SupabaseKey:           os.Getenv("SUPABASE_SERVICE_KEY"),
		WhatsAppDB:            env("WHATSAPP_DB", "file:/data/whatsmeow.db?_foreign_keys=on"),
		WhatsAppDialect:       env("WHATSAPP_DB_DIALECT", "sqlite3"),
		AutoReply:             envBool("WHATSAPP_AUTO_REPLY", true),
		PollInterval:          envDuration("WHATSAPP_POLL_INTERVAL", 3*time.Second),
		SendGap:               envDuration("WHATSAPP_SEND_GAP", 1500*time.Millisecond),
		WorkerName:            env("BRIDGE_WORKER_NAME", defaultHost(hostname())),
		LogLevel:              env("LOG_LEVEL", "info"),
		RazorpayKeyID:         os.Getenv("RAZORPAY_KEY_ID"),
		RazorpayKeySecret:     os.Getenv("RAZORPAY_KEY_SECRET"),
		RazorpayWebhookSecret: os.Getenv("RAZORPAY_WEBHOOK_SECRET"),
	}

	var missing []string
	if c.SupabaseURL == "" {
		missing = append(missing, "SUPABASE_URL")
	}
	if c.SupabaseKey == "" {
		missing = append(missing, "SUPABASE_SERVICE_KEY")
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	if !strings.HasPrefix(c.SupabaseURL, "https://") && !strings.HasPrefix(c.SupabaseURL, "http://localhost") {
		return c, fmt.Errorf("SUPABASE_URL must be https (got %q)", c.SupabaseURL)
	}
	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "bridge"
	}
	return h
}

func defaultHost(h string) string {
	if i := strings.IndexByte(h, '.'); i > 0 {
		return h[:i]
	}
	return h
}
