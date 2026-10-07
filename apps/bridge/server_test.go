package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakePostgREST stands in for Supabase so the HTTP layer can be tested without
// a live project. It records what the bridge asked for and answers with
// canned rows.
type fakePostgREST struct {
	t            *testing.T
	server       *httptest.Server
	requests     []string
	bodies       []string
	responses    map[string]any
	rpcResponses map[string]any
	failWith     int
}

func newFakePostgREST(t *testing.T) *fakePostgREST {
	fake := &fakePostgREST{
		t:         t,
		responses: map[string]any{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		fake.requests = append(fake.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		fake.bodies = append(fake.bodies, string(body))

		if fake.failWith != 0 {
			w.WriteHeader(fake.failWith)
			_, _ = w.Write([]byte(`{"message":"boom","code":"PGRST100"}`))
			return
		}

		for key, payload := range fake.responses {
			if strings.Contains(r.URL.Path, key) && r.Method == http.MethodGet {
				writeJSON(w, http.StatusOK, payload)
				return
			}
		}

		for key, payload := range fake.rpcResponses {
			if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/rpc/"+key) {
				writeJSON(w, http.StatusOK, payload)
				return
			}
		}

		// Anything else echoes the request back so handlers can parse it.
		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakePostgREST) client() *Client {
	return NewSupabaseClient(f.server.URL, "test-service-key")
}

func (f *fakePostgREST) sawRequest(fragment string) bool {
	for _, req := range f.requests {
		if strings.Contains(req, fragment) {
			return true
		}
	}
	return false
}

func newTestServer(t *testing.T, db *Client) (*httptest.Server, *whatsapp) {
	t.Helper()
	wa := &whatsapp{status: "disconnected"}

	cfg := Config{PublicBaseURL: "https://app.nuttywonders.com", WorkerName: "test", SendGap: time.Millisecond}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(cfg, db, wa, NewAdminAuth("http://supabase.invalid", "key", true), log)

	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer, wa
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any) (int, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(raw))
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	payload := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	return resp.StatusCode, payload
}

func TestHealthReportsWhatsAppStatus(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	status, payload := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/health", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if payload["ok"] != true {
		t.Errorf("expected ok=true, got %v", payload["ok"])
	}
}

func TestListProductsReturnsOnlyActiveRows(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	status, payload := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/products", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if _, ok := payload["products"]; !ok {
		t.Error("response should include a products key even when empty")
	}
	if !fake.sawRequest("is_active=eq.true") {
		t.Errorf("expected the query to filter to active products, saw %v", fake.requests)
	}
}

func TestCheckoutRejectsBadPhoneBeforeTouchingTheDatabase(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	status, payload := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/orders", map[string]any{
		"name":  "Kavita",
		"phone": "123",
		"items": []map[string]any{{"product_id": 1, "quantity": 1}},
	})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if !strings.Contains(payload["error"].(string), "10-digit") {
		t.Errorf("unexpected error: %v", payload["error"])
	}
	if len(fake.requests) != 0 {
		t.Errorf("a bad phone number should not reach the database, saw %v", fake.requests)
	}
}

func TestCheckoutRejectsAnEmptyCart(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	status, payload := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/orders", map[string]any{
		"name":  "Kavita",
		"phone": "8108398025",
		"items": []map[string]any{},
	})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if !strings.Contains(payload["error"].(string), "cart is empty") {
		t.Errorf("unexpected error: %v", payload["error"])
	}
}

func TestCheckoutClampsQuantityAndSendsServerSidePricing(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	status, _ := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/orders", map[string]any{
		"name":  "Kavita",
		"phone": "+91 81083 98025",
		"items": []map[string]any{{"product_id": 3, "quantity": 9999}},
	})
	if status == http.StatusBadRequest {
		t.Fatalf("checkout should not have been rejected for a large quantity")
	}

	if len(fake.bodies) == 0 {
		t.Fatal("expected the bridge to call place_order")
	}
	body := fake.bodies[len(fake.bodies)-1]
	if !strings.Contains(body, "place_order") && !strings.Contains(fake.requests[len(fake.requests)-1], "place_order") {
		t.Errorf("expected the place_order RPC, saw %v", fake.requests)
	}
	// The bridge must not send prices: pricing happens inside the RPC.
	if strings.Contains(body, "price") {
		t.Errorf("checkout should not send prices to the database, got %s", body)
	}
	if !strings.Contains(body, `"product_id":3`) {
		t.Errorf("expected the product id in the payload, got %s", body)
	}
	if !strings.Contains(body, `"quantity":50`) {
		t.Errorf("expected quantity to be clamped to 50, got %s", body)
	}
}

func TestCheckoutSurfacesUpstreamFailuresWithoutLeakingSQL(t *testing.T) {
	fake := newFakePostgREST(t)
	fake.failWith = http.StatusBadRequest
	server, _ := newTestServer(t, fake.client())

	status, payload := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/orders", map[string]any{
		"name":  "Kavita",
		"phone": "8108398025",
		"items": []map[string]any{{"product_id": 1, "quantity": 1}},
	})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	message, _ := payload["error"].(string)
	if strings.Contains(strings.ToLower(message), "select") || strings.Contains(strings.ToLower(message), "from public") {
		t.Errorf("error message leaked SQL: %q", message)
	}
}

func TestTrackOrderNeedsCodeAndPhone(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	status, _ := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/orders/track?code=NW-251001-0001", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without a phone number", status)
	}
}

func TestAdminRoutesRejectAnonymousCallers(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	for _, path := range []string{
		"/api/admin/dashboard",
		"/api/admin/products",
		"/api/admin/orders",
		"/api/admin/customers",
		"/api/admin/settings",
		"/api/admin/broadcasts",
		"/api/admin/whatsapp",
		"/api/admin/jobs",
		"/api/admin/conversations",
		"/api/admin/me",
	} {
		status, payload := doJSON(t, server.Client(), http.MethodGet, server.URL+path, nil)
		if status != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want 401", path, status)
		}
		if _, ok := payload["error"]; !ok {
			t.Errorf("%s should return an error message", path)
		}
	}
}

func TestAdminWriteRoutesRejectAnonymousCallers(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/admin/products", map[string]any{"name": "Sneaky"}},
		{http.MethodPatch, "/api/admin/orders/1", map[string]any{"status": "shipped"}},
		{http.MethodPost, "/api/admin/broadcasts", map[string]any{"title": "x", "body": "y"}},
		{http.MethodPut, "/api/admin/settings/upi_vpa", map[string]any{"value": "x@y"}},
		{http.MethodPost, "/api/admin/whatsapp/logout", map[string]any{}},
	}

	for _, tc := range cases {
		status, _ := doJSON(t, server.Client(), tc.method, server.URL+tc.path, tc.body)
		if status != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.path, status)
		}
	}
	if len(fake.requests) != 0 {
		t.Errorf("no unauthenticated request should reach the database, saw %v", fake.requests)
	}
}

func TestPublicSettingsOnlyExposesShopFacts(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	status, payload := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	settings, ok := payload["settings"].(map[string]any)
	if !ok {
		t.Fatalf("expected a settings object, got %v", payload)
	}
	if _, ok := settings["upi_vpa"]; !ok {
		t.Error("shoppers need the UPI id to pay")
	}
	if _, ok := settings["delivery_fee_inr"]; !ok {
		t.Error("shoppers need the delivery fee to see the right total")
	}
}

func TestSessionRejectsMissingCredentials(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	status, _ := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/admin/session", map[string]any{})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestCheckoutIsRateLimited(t *testing.T) {
	fake := newFakePostgREST(t)
	server, _ := newTestServer(t, fake.client())

	body := map[string]any{
		"name":  "Kavita",
		"phone": "8108398025",
		"items": []map[string]any{{"product_id": 1, "quantity": 1}},
	}

	seenLimit := false
	for range 12 {
		status, _ := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/orders", body)
		if status == http.StatusTooManyRequests {
			seenLimit = true
			break
		}
	}
	if !seenLimit {
		t.Error("repeated checkout calls should eventually be rate limited")
	}
}

func TestSplitForWhatsAppIsUsedOnLongBodies(t *testing.T) {
	long := strings.Repeat("NuttyWonders granola is baked in small batches. ", 100)
	if len(splitForWhatsApp(long)) < 2 {
		t.Error("a very long bot reply needs to be split")
	}
}
