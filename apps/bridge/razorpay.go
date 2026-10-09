package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const razorpayEndpoint = "https://api.razorpay.com/v1"

// razorpayAPIBase is overridable in tests so checkout flows never touch the
// live gateway.
var razorpayAPIBase = razorpayEndpoint

type razorpayClient struct {
	keyID         string
	keySecret     string
	webhookSecret string
	http          *http.Client
}

func (s *Server) razorpay() *razorpayClient {
	return &razorpayClient{
		keyID:         s.cfg.RazorpayKeyID,
		keySecret:     s.cfg.RazorpayKeySecret,
		webhookSecret: s.cfg.RazorpayWebhookSecret,
		http:          &http.Client{Timeout: 15 * time.Second},
	}
}

// configured reports whether both sides of the live key pair exist. The key
// secret stays on the server; only key_id ever reaches the browser.
func (c *razorpayClient) configured() bool {
	return c.keyID != "" && c.keySecret != ""
}

// createOrder asks Razorpay for an order of the exact paid-in-INR amount. The
// returned id is the only thing the checkout modal needs from the gateway.
func (c *razorpayClient) createOrder(ctx context.Context, amountPaise int64, receipt string) (string, error) {
	if amountPaise < 100 {
		return "", errors.New("amount must be at least 100 paise")
	}
	raw, err := json.Marshal(map[string]any{
		"amount":   amountPaise,
		"currency": "INR",
		"receipt":  receipt,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, razorpayAPIBase+"/orders", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", &razorpayError{Status: resp.StatusCode, Body: string(body)}
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", errors.New("razorpay did not return an order id")
	}
	return created.ID, nil
}

type razorpayError struct {
	Status int
	Body   string
}

func (e *razorpayError) Error() string {
	body := e.Body
	if len(body) > 200 {
		body = body[:200]
	}
	return "razorpay " + strconv.Itoa(e.Status) + ": " + body
}

// verifyPaymentSignature checks the payment.success callback from the browser
// against the gateway secret, exactly as Razorpay documents:
//
//	signature = HMAC-SHA256(order_id + "|" + payment_id, key_secret)
func (c *razorpayClient) verifyPaymentSignature(orderID, paymentID, signature string) bool {
	return hmacHexEqual(c.keySecret, orderID+"|"+paymentID, signature)
}

// verifyWebhook validates the X-Razorpay-Signature header over the raw body.
func (c *razorpayClient) verifyWebhook(body []byte, signature string) bool {
	return hmacHexEqual(c.webhookSecret, string(body), signature)
}

func hmacHexEqual(secret, message, expected string) bool {
	if secret == "" || expected == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	sum := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sum), []byte(strings.ToLower(expected)))
}

// handleVerifyPayment is called by the storefront after Razorpay's
// payment.success callback. The signature is un-forgeable proof the gateway
// charged the shopper, so matching it is enough to mark the order paid.
func (s *Server) handleVerifyPayment(w http.ResponseWriter, r *http.Request) {
	rz := s.razorpay()
	if !rz.configured() {
		writeError(w, http.StatusBadRequest, "online payments are not enabled")
		return
	}

	var req struct {
		RazorpayOrderID   string `json:"razorpay_order_id"`
		RazorpayPaymentID string `json:"razorpay_payment_id"`
		RazorpaySignature string `json:"razorpay_signature"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RazorpayOrderID == "" || req.RazorpayPaymentID == "" || req.RazorpaySignature == "" {
		writeError(w, http.StatusBadRequest, "incomplete payment confirmation")
		return
	}
	if !rz.verifyPaymentSignature(req.RazorpayOrderID, req.RazorpayPaymentID, req.RazorpaySignature) {
		s.log.Warn("rejected a payment signature", "order", req.RazorpayOrderID)
		writeError(w, http.StatusBadRequest, "payment could not be verified")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	order, alreadyPaid, err := s.markOrderPaid(ctx, req.RazorpayOrderID, req.RazorpayPaymentID)
	if err != nil {
		if errors.Is(err, errOrderNotFound) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		s.writeUpstreamError(w, err)
		return
	}
	if !alreadyPaid {
		s.queuePaymentReceived(ctx, &order)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"order":   order,
		"payment": map[string]any{"status": "paid"},
	})
}

// handleRazorpayWebhookInfo answers browsers that stumble onto the webhook
// URL. Razorpay only ever POSTs here, so a GET is just a reachability check.
// Returning 200 instead of Go's bare 405 keeps the endpoint from looking
// broken when someone clicks it.
func (s *Server) handleRazorpayWebhookInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "NuttyWonders Razorpay webhook endpoint. It accepts POST requests from Razorpay for payment.captured and order.paid events. A GET only proves this URL is reachable.")
}

// handleRazorpayWebhook receives Razorpay's server-side payment events. Only
// the raw body handed to us plus Razorpay's signature can authenticate these,
// so the handler reads the body itself instead of decodeJSON.
func (s *Server) handleRazorpayWebhook(w http.ResponseWriter, r *http.Request) {
	rz := s.razorpay()
	if !rz.configured() || rz.webhookSecret == "" {
		writeError(w, http.StatusBadRequest, "webhooks are not configured")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil || len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty webhook body")
		return
	}
	if !rz.verifyWebhook(body, r.Header.Get("X-Razorpay-Signature")) {
		s.log.Warn("rejected a webhook signature")
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var hook struct {
		Event   string `json:"event"`
		Payload struct {
			Order struct {
				Entity struct {
					ID string `json:"id"`
				} `json:"entity"`
			} `json:"order"`
			Payment struct {
				Entity struct {
					ID      string `json:"id"`
					OrderID string `json:"order_id"`
					Status  string `json:"status"`
				} `json:"entity"`
			} `json:"payment"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &hook); err != nil {
		writeError(w, http.StatusBadRequest, "malformed webhook payload")
		return
	}

	// payment.failed and friends are acknowledged with 200 so Razorpay stops
	// pinging us, but they change no order state.
	if hook.Event != "payment.captured" && hook.Event != "payment.authorized" && hook.Event != "order.paid" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "handled": false})
		return
	}

	orderID := hook.Payload.Order.Entity.ID
	paymentID := hook.Payload.Payment.Entity.ID
	if orderID == "" {
		orderID = hook.Payload.Payment.Entity.OrderID
	}
	if orderID == "" {
		s.log.Warn("payment event without an order", "event", hook.Event)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "handled": false})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	order, alreadyPaid, err := s.markOrderPaid(ctx, orderID, paymentID)
	if err != nil {
		// An order we do not track is not an outage: acknowledge so Razorpay
		// stops retrying, mirroring how the payment events are at-least-once.
		if errors.Is(err, errOrderNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "handled": false})
			return
		}
		s.writeUpstreamError(w, err)
		return
	}
	if !alreadyPaid {
		s.queuePaymentReceived(ctx, &order)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "handled": true, "code": order.Code})
}

// markOrderPaid finds the local order behind a Razorpay order id and moves it
// to paid. Replaying an already-paid order is a no-op: webhooks are
// at-least-once and the storefront may have verified the same payment first.
func (s *Server) markOrderPaid(ctx context.Context, rzOrderID, rzPaymentID string) (Order, bool, error) {
	var orders []Order
	query := url.Values{}
	query.Set("payment_ref", "eq."+rzOrderID)
	query.Set("select", orderSelect)
	query.Set("limit", "1")
	if err := s.db.Select(ctx, "orders", query, &orders); err != nil {
		return Order{}, false, err
	}
	if len(orders) == 0 {
		return Order{}, false, errOrderNotFound
	}

	order := orders[0]
	if order.PaymentStatus == "paid" {
		return order, true, nil
	}

	body := map[string]any{"payment_status": "paid"}
	if rzPaymentID != "" {
		body["payment_transaction_id"] = rzPaymentID
	}

	var updated []Order
	uquery := url.Values{}
	uquery.Set("id", "eq."+strconv.FormatInt(order.ID, 10))
	if err := s.db.Update(ctx, "orders", uquery, body, &updated); err != nil {
		return Order{}, false, err
	}
	if len(updated) == 1 {
		order = updated[0]
	} else if fresh, err := s.queryOrders(ctx, url.Values{"id": {"eq." + strconv.FormatInt(order.ID, 10)}}); err == nil && len(fresh) == 1 {
		order = fresh[0]
	}
	return order, false, nil
}

func (s *Server) queuePaymentReceived(ctx context.Context, order *Order) {
	total := strconv.FormatFloat(order.TotalINR, 'f', 2, 64)
	body := "Payment received for NuttyWonders order " + order.Code +
		" — ₹" + total + ". We're packing it up now!"
	_ = s.db.Insert(ctx, "whatsapp_jobs", map[string]any{
		"to_phone": order.PhoneE164,
		"body":     body,
		"kind":     "order_update",
		"order_id": order.ID,
	}, nil)
}

// attachRazorpayOrder creates a gateway order for the placed order and, on
// success, records it on the row. It never fails the checkout: a gateway blip
// falls back to the manual UPI instructions. The paise amount is derived from
// the server-side total, never from the browser.
func (s *Server) attachRazorpayOrder(ctx context.Context, order *Order) (rzOrderID string, amountPaise int64, ok bool) {
	rz := s.razorpay()
	if !rz.configured() {
		return "", 0, false
	}

	amountPaise = int64(math.Round(order.TotalINR * 100))
	rzOrderID, err := rz.createOrder(ctx, amountPaise, order.Code)
	if err != nil {
		s.log.Warn("razorpay order creation failed, falling back to manual payment",
			"code", order.Code, "error", err)
		return "", 0, false
	}

	query := url.Values{}
	query.Set("id", "eq."+strconv.FormatInt(order.ID, 10))
	_ = s.db.Update(ctx, "orders", query, map[string]any{
		"payment_gateway": "razorpay",
		"payment_ref":     rzOrderID,
	}, nil)
	return rzOrderID, amountPaise, true
}
