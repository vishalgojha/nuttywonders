package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newRazorpayTestServer(t *testing.T, db *Client, keyID, keySecret, webhookSecret string) *httptest.Server {
	t.Helper()
	wa := &whatsapp{status: "disconnected"}
	cfg := Config{
		PublicBaseURL:         "https://app.nuttywonders.com",
		WorkerName:            "test",
		SendGap:               time.Millisecond,
		RazorpayKeyID:         keyID,
		RazorpayKeySecret:     keySecret,
		RazorpayWebhookSecret: webhookSecret,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(cfg, db, wa, NewAdminAuth("http://supabase.invalid", "key", true), log)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer
}

func doRaw(t *testing.T, client *http.Client, method, url string, raw []byte, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	payload := map[string]any{}
	body, _ := io.ReadAll(resp.Body)
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}
	return resp.StatusCode, payload
}

func rzSignature(secret, message string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyPaymentSignatureMatchesKnownVector(t *testing.T) {
	rz := &razorpayClient{keySecret: "rzp_live_shhh"}
	if !rz.verifyPaymentSignature("order_ABC", "pay_XYZ", "903e87f9039eeb6d95e5c63b5140fe4bb16ef8684fdd021cbc2388a2d3400aed") {
		t.Error("a correct signature must verify")
	}
	if rz.verifyPaymentSignature("order_ABC", "pay_XYZ", "deadbeef") {
		t.Error("a wrong signature must be rejected")
	}
	if rz.verifyPaymentSignature("order_ABC", "pay_XYZ", "") {
		t.Error("an empty signature must be rejected")
	}
}

func TestVerifyWebhookMatchesKnownVector(t *testing.T) {
	rz := &razorpayClient{webhookSecret: "whsec_123"}
	body := []byte(`{"event":"payment.captured"}`)
	if !rz.verifyWebhook(body, "00b41411be102026dd9650c17a59cd79d2fa19427df96c2e5ccdd7753bcc481b") {
		t.Error("a correct webhook signature must verify")
	}
	if rz.verifyWebhook(body, "00b41411be102026dd9650c17a59cd79d2fa19427df96c2e5ccdd7753bcc481c") {
		t.Error("a wrong webhook signature must be rejected")
	}
}

func TestCreateOrderRejectsTinyAmount(t *testing.T) {
	rz := &razorpayClient{keyID: "rzp_test_k", keySecret: "secret"}
	if _, err := rz.createOrder(context.Background(), 99, "NW-0001"); err == nil {
		t.Error("amounts below 100 paise must be rejected before any request")
	}
}

func orderRow() map[string]any {
	return map[string]any{
		"id":                     int64(5),
		"code":                   "NW-261007-0001",
		"phone_e164":             "+918108398025",
		"customer_name":          "Kavita",
		"channel":                "web",
		"status":                 "new",
		"payment_status":         "pending",
		"subtotal_inr":           float64(430),
		"discount_inr":           float64(0),
		"shipping_inr":           float64(60),
		"total_inr":              float64(490),
		"payment_ref":            "order_ABC",
		"payment_transaction_id": nil,
		"created_at":             time.Now().UTC().Format(time.RFC3339),
	}
}

func fakeRazorpay(t *testing.T, createdID string, fail bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_, _ = io.Copy(io.Discard, r.Body)
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST_ERROR"}}`))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":       createdID,
			"amount":   49000,
			"currency": "INR",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckoutReturnsRazorpayPaymentNeverLeakingTheSecret(t *testing.T) {
	gateway := fakeRazorpay(t, "order_test_001", false)
	oldBase := razorpayAPIBase
	razorpayAPIBase = gateway.URL
	t.Cleanup(func() { razorpayAPIBase = oldBase })

	fake := newFakePostgREST(t)
	fake.rpcResponses = map[string]any{
		"place_order": map[string]any{
			"order": orderRow(),
			"items": []any{},
		},
	}
	server := newRazorpayTestServer(t, fake.client(), "rzp_test_xyz", "supersecret", "")

	status, payload := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/orders", map[string]any{
		"name":  "Kavita",
		"phone": "8108398025",
		"items": []map[string]any{{"product_id": 1, "quantity": 1}},
	})
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}

	payment, _ := payload["payment"].(map[string]any)
	if payment["method"] != "razorpay" {
		t.Fatalf("expected razorpay method, got %v", payment["method"])
	}
	rzp, _ := payment["razorpay"].(map[string]any)
	if rzp["order_id"] != "order_test_001" {
		t.Errorf("expected the gateway order id, got %v", rzp["order_id"])
	}
	if rzp["key_id"] != "rzp_test_xyz" {
		t.Errorf("expected key_id for the browser, got %v", rzp["key_id"])
	}

	raw, _ := json.Marshal(payload)
	if strings.Contains(string(raw), "supersecret") {
		t.Error("the key secret must never reach the client")
	}
	if _, ok := rzp["key_secret"]; ok {
		t.Error("the key secret must not be present under any name")
	}

	found := false
	for _, body := range fake.bodies {
		if strings.Contains(body, "order_test_001") && strings.Contains(body, "payment_ref") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the gateway order id to be saved on the order, bodies: %v", fake.bodies)
	}
}

func TestCheckoutFallsBackToUPIWhenGatewayFails(t *testing.T) {
	gateway := fakeRazorpay(t, "", true)
	oldBase := razorpayAPIBase
	razorpayAPIBase = gateway.URL
	t.Cleanup(func() { razorpayAPIBase = oldBase })

	fake := newFakePostgREST(t)
	fake.rpcResponses = map[string]any{
		"place_order": map[string]any{"order": orderRow(), "items": []any{}},
	}
	server := newRazorpayTestServer(t, fake.client(), "rzp_test_xyz", "supersecret", "")

	status, payload := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/orders", map[string]any{
		"name":  "Kavita",
		"phone": "8108398025",
		"items": []map[string]any{{"product_id": 1, "quantity": 1}},
	})
	if status != http.StatusCreated {
		t.Fatalf("a gateway blip must not kill checkout, status = %d", status)
	}
	payment, _ := payload["payment"].(map[string]any)
	if payment["method"] != "upi" {
		t.Fatalf("expected manual UPI fallback, got %v", payment["method"])
	}
	if _, ok := payment["razorpay"]; ok {
		t.Error("no razorpay payload should be sent when the gateway failed")
	}
}

func TestVerifyPaymentMarksPaidAndQueuesANotification(t *testing.T) {
	fake := newFakePostgREST(t)
	fake.responses["/orders"] = []any{orderRow()}
	server := newRazorpayTestServer(t, fake.client(), "rzp_test_xyz", "rzp_live_shhh", "")

	status, payload := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/orders/verify", map[string]any{
		"razorpay_order_id":   "order_ABC",
		"razorpay_payment_id": "pay_XYZ",
		"razorpay_signature":  "903e87f9039eeb6d95e5c63b5140fe4bb16ef8684fdd021cbc2388a2d3400aed",
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	payment, _ := payload["payment"].(map[string]any)
	if payment["status"] != "paid" {
		t.Errorf("expected a paid payment status, got %v", payment["status"])
	}

	sawPatch := false
	sawJob := false
	for i, body := range fake.bodies {
		switch {
		case strings.Contains(fake.requests[i], "PATCH") && strings.Contains(body, `"payment_status":"paid"`):
			sawPatch = true
		case strings.Contains(fake.requests[i], "POST") && strings.Contains(fake.requests[i], "whatsapp_jobs"):
			sawJob = true
		}
	}
	if !sawPatch {
		t.Errorf("expected the order to be marked paid, requests: %v", fake.requests)
	}
	if !sawJob {
		t.Error("expected a payment-received WhatsApp notification to be queued")
	}
}

func TestVerifyPaymentRejectsABadSignatureWithoutWriting(t *testing.T) {
	fake := newFakePostgREST(t)
	fake.responses["/orders"] = []any{orderRow()}
	server := newRazorpayTestServer(t, fake.client(), "rzp_test_xyz", "rzp_live_shhh", "")

	status, _ := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/orders/verify", map[string]any{
		"razorpay_order_id":   "order_ABC",
		"razorpay_payment_id": "pay_XYZ",
		"razorpay_signature":  "not-a-real-signature",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	for i, req := range fake.requests {
		if strings.Contains(req, "PATCH") {
			t.Errorf("a bad signature must not touch the database: %s %s", req, fake.bodies[i])
		}
	}
}

func TestRazorpayWebhookRejectsBadSignatures(t *testing.T) {
	fake := newFakePostgREST(t)
	server := newRazorpayTestServer(t, fake.client(), "rzp_test_xyz", "rzp_live_shhh", "whsec_123")

	body := []byte(`{"event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_1","order_id":"order_1"}}}}`)
	status, _ := doRaw(t, server.Client(), http.MethodPost, server.URL+"/api/webhooks/razorpay", body, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("a webhook without a signature must be rejected, status = %d", status)
	}
	status, _ = doRaw(t, server.Client(), http.MethodPost, server.URL+"/api/webhooks/razorpay", body,
		map[string]string{"X-Razorpay-Signature": "deadbeef"})
	if status != http.StatusUnauthorized {
		t.Fatalf("a wrong webhook signature must be rejected, status = %d", status)
	}
	if len(fake.requests) != 0 {
		t.Errorf("rejected webhooks must not reach the database: %v", fake.requests)
	}
}

func TestRazorpayWebhookMarksPaidForCapturedPayment(t *testing.T) {
	fake := newFakePostgREST(t)
	fake.responses["/orders"] = []any{orderRow()}
	server := newRazorpayTestServer(t, fake.client(), "rzp_test_xyz", "rzp_live_shhh", "whsec_123")

	body := []byte(fmt.Sprintf(`{"event":"payment.captured","payload":{"order":{"entity":{"id":"order_ABC"}},"payment":{"entity":{"id":"pay_XYZ","order_id":"order_ABC","status":"captured"}}}}`))
	sig := rzSignature("whsec_123", string(body))
	status, payload := doRaw(t, server.Client(), http.MethodPost, server.URL+"/api/webhooks/razorpay", body,
		map[string]string{"X-Razorpay-Signature": sig})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if payload["handled"] != true {
		t.Errorf("expected the webhook to be handled, got %v", payload)
	}

	sawPatch := false
	for i, req := range fake.requests {
		if strings.Contains(req, "PATCH") && strings.Contains(fake.bodies[i], `"payment_status":"paid"`) {
			sawPatch = true
		}
	}
	if !sawPatch {
		t.Errorf("expected the webhook to mark the order paid: %v", fake.requests)
	}
}

func TestRazorpayWebhookAcknowledgesUnhandledEvents(t *testing.T) {
	fake := newFakePostgREST(t)
	server := newRazorpayTestServer(t, fake.client(), "rzp_test_xyz", "rzp_live_shhh", "whsec_123")

	body := []byte(`{"event":"payment.failed","payload":{"payment":{"entity":{"id":"pay_1","order_id":"order_1","status":"failed"}}}}`)
	sig := rzSignature("whsec_123", string(body))
	status, payload := doRaw(t, server.Client(), http.MethodPost, server.URL+"/api/webhooks/razorpay", body,
		map[string]string{"X-Razorpay-Signature": sig})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if payload["handled"] != false {
		t.Errorf("a failed-payment event must be acknowledged without changing state, got %v", payload)
	}
	if len(fake.requests) != 0 {
		t.Errorf("unhandled events must not reach the database: %v", fake.requests)
	}
}
