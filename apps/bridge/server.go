package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	cfg   Config
	db    *Client
	wa    *whatsapp
	auth  *AdminAuth
	log   *slog.Logger
	limit *rateLimiter
}

func NewServer(cfg Config, db *Client, wa *whatsapp, auth *AdminAuth, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, db: db, wa: wa, auth: auth, log: log, limit: newRateLimiter()}
	wa.inbound = s.handleInboundWhatsApp
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/products", s.handleListProducts)
	mux.HandleFunc("GET /api/settings", s.handlePublicSettings)
	mux.HandleFunc("POST /api/orders", s.limit.limit("checkout", 10, time.Minute, s.handleCheckout))
	mux.HandleFunc("POST /api/orders/verify", s.limit.limit("payment", 20, time.Minute, s.handleVerifyPayment))
	mux.HandleFunc("GET /api/orders/track", s.handleTrackOrder)
	mux.HandleFunc("POST /api/webhooks/razorpay", s.handleRazorpayWebhook)
	mux.HandleFunc("GET /api/webhooks/razorpay", s.handleRazorpayWebhookInfo)
	mux.HandleFunc("POST /api/admin/session", s.limit.limit("signin", 8, time.Minute, s.handleSignIn))
	mux.HandleFunc("POST /api/admin/session/refresh", s.handleRefreshSession)
	mux.HandleFunc("POST /api/admin/session/sign-out", s.handleSignOut)

	mux.Handle("GET /api/admin/me", s.auth.requireAdmin(s.handleAdminMe))
	mux.Handle("GET /api/admin/dashboard", s.auth.requireAdmin(s.handleDashboard))
	mux.Handle("GET /api/admin/products", s.auth.requireAdmin(s.handleAdminListProducts))
	mux.Handle("POST /api/admin/products", s.auth.requireAdmin(s.handleAdminSaveProduct))
	mux.Handle("GET /api/admin/orders", s.auth.requireAdmin(s.handleAdminListOrders))
	mux.Handle("GET /api/admin/orders/{id}", s.auth.requireAdmin(s.handleAdminGetOrder))
	mux.Handle("PATCH /api/admin/orders/{id}", s.auth.requireAdmin(s.handleAdminUpdateOrder))
	mux.Handle("POST /api/admin/orders", s.auth.requireAdmin(s.handleAdminCreateOrder))
	mux.Handle("POST /api/admin/orders/{id}/resend", s.auth.requireAdmin(s.handleAdminResendOrder))
	mux.Handle("GET /api/admin/customers", s.auth.requireAdmin(s.handleAdminListCustomers))
	mux.Handle("PATCH /api/admin/customers/{id}", s.auth.requireAdmin(s.handleAdminUpdateCustomer))
	mux.Handle("GET /api/admin/conversations", s.auth.requireAdmin(s.handleAdminConversations))
	mux.Handle("GET /api/admin/jobs", s.auth.requireAdmin(s.handleAdminListJobs))
	mux.Handle("GET /api/admin/settings", s.auth.requireAdmin(s.handleAdminSettings))
	mux.Handle("PUT /api/admin/settings/{key}", s.auth.requireAdmin(s.handleAdminSaveSetting))
	mux.Handle("GET /api/admin/broadcasts", s.auth.requireAdmin(s.handleAdminListBroadcasts))
	mux.Handle("POST /api/admin/broadcasts", s.auth.requireAdmin(s.handleAdminCreateBroadcast))
	mux.Handle("GET /api/admin/whatsapp", s.auth.requireAdmin(s.handleWhatsAppStatus))
	mux.Handle("POST /api/admin/whatsapp/pair-code", s.auth.requireAdmin(s.handleWhatsAppPairCode))
	mux.Handle("POST /api/admin/whatsapp/logout", s.auth.requireAdmin(s.handleWhatsAppLogout))
	mux.Handle("POST /api/admin/whatsapp/send", s.auth.requireAdmin(s.handleWhatsAppSend))
	mux.Handle("POST /api/admin/whatsapp/name", s.auth.requireAdmin(s.handleWhatsAppRename))
	mux.Handle("POST /api/admin/retention/run", s.auth.requireAdmin(s.handleRunRetention))

	return withRecover(s.log, withRequestLog(s.log, mux))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

// writeUpstreamError maps a Supabase failure onto an HTTP status without
// leaking table names or SQL to the browser.
func (s *Server) writeUpstreamError(w http.ResponseWriter, err error) {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		s.log.Warn("supabase error", "status", apiErr.Status, "message", apiErr.Message)
		switch apiErr.Status {
		case http.StatusNotFound:
			writeError(w, http.StatusNotFound, "not found")
		case http.StatusConflict:
			writeError(w, http.StatusConflict, friendlyConstraint(apiErr.Message))
		case http.StatusBadRequest:
			writeError(w, http.StatusBadRequest, friendlyConstraint(apiErr.Message))
		default:
			writeError(w, http.StatusBadGateway, "the shop database is not reachable right now")
		}
		return
	}
	s.log.Error("request failed", "error", err)
	writeError(w, http.StatusBadGateway, "the shop database is not reachable right now")
}

func friendlyConstraint(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "check constraint"), strings.Contains(lower, "invalid input value"):
		return "that value is not allowed: " + message
	case strings.Contains(lower, "invalid phone"):
		return "please enter a valid 10-digit phone number"
	case strings.Contains(lower, "unavailable"):
		return message
	default:
		return message
	}
}

const maxBodyBytes = 256 << 10

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request: "+trimJSONError(err))
		return false
	}
	return true
}

func trimJSONError(err error) string {
	msg := err.Error()
	if len(msg) > 140 {
		return msg[:140] + "…"
	}
	return msg
}

func pathInt64(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func withRequestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.Method != http.MethodGet || rec.status >= 400 || time.Since(start) > time.Second {
			log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start).Round(time.Millisecond))
		}
	})
}

func withRecover(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic in handler", "path", r.URL.Path, "panic", rec)
				writeError(w, http.StatusInternalServerError, "something went wrong on our side")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
