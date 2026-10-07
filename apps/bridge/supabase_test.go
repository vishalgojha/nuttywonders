package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The fake PostgREST in server_test.go has no way to send response headers, and
// both of these paths read the answer from the response rather than the body, so
// they get their own servers here.

func TestCountReadsTheTotalFromContentRange(t *testing.T) {
	var seen *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(r.Context())
		w.Header().Set("Content-Range", "0-0/57")
		_, _ = w.Write([]byte(`[{"id":"00000000-0000-0000-0000-000000000001"}]`))
	}))
	defer server.Close()

	client := NewSupabaseClient(server.URL, "test-service-key")

	got, err := client.Count(context.Background(), "orders", url.Values{"status": {"new,paid"}})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 57 {
		t.Fatalf("Count = %d, want 57", got)
	}
	if seen == nil {
		t.Fatal("no request reached the server")
	}
	if prefer := seen.Header.Get("Prefer"); prefer != "count=exact" {
		t.Errorf("Prefer = %q, want count=exact", prefer)
	}
	if select_ := seen.URL.Query().Get("select"); select_ != "id" {
		t.Errorf("select = %q, want id", select_)
	}
	if limit := seen.URL.Query().Get("limit"); limit != "1" {
		t.Errorf("limit = %q, want 1: counting should not pull the rows back", limit)
	}
	if status := seen.URL.Query().Get("status"); status != "new,paid" {
		t.Errorf("status = %q, want the caller's filter", status)
	}
}

func TestCountHandlesAnEmptyTable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "*/0")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	got, err := NewSupabaseClient(server.URL, "key").Count(context.Background(), "orders", nil)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
}

func TestCountDoesNotCallAnEmptyTableZeroWhenTheHeaderIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	// A missing Content-Range means the count is unknown, not zero. Reporting
	// zero here would quietly blank out the admin dashboard.
	if _, err := NewSupabaseClient(server.URL, "key").Count(context.Background(), "orders", nil); err == nil {
		t.Fatal("Count succeeded without a Content-Range header, want an error")
	}
}

func TestRPCScalarDecodesABareNumber(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, 256)
		n, _ := r.Body.Read(raw)
		body = string(raw[:n])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`4`))
	}))
	defer server.Close()

	got, err := NewSupabaseClient(server.URL, "key").RPCScalar(context.Background(), "expire_stale_orders", nil)
	if err != nil {
		t.Fatalf("RPCScalar: %v", err)
	}
	if got != 4 {
		t.Fatalf("RPCScalar = %d, want 4", got)
	}
	if !strings.Contains(body, "expire_stale_orders") && body != "" {
		t.Errorf("request body = %q, want the function payload", body)
	}
}

func TestRPCScalarRejectsAnArrayResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"expire_stale_orders": 4}]`))
	}))
	defer server.Close()

	// A row shaped response means the function is not the scalar one this code
	// expects; silently decoding it as 0 would hide a broken migration.
	if _, err := NewSupabaseClient(server.URL, "key").RPCScalar(context.Background(), "expire_stale_orders", nil); err == nil {
		t.Fatal("RPCScalar accepted an array result, want an error")
	}
}

func TestTrackOrderReturnsNotFoundWhenNothingMatches(t *testing.T) {
	fake := newFakePostgREST(t)
	fake.responses["/rest/v1/rpc/track_order"] = []any{}

	server, _ := newTestServer(t, fake.client())
	client := server.Client()

	status, payload := doJSON(t, client, http.MethodGet, server.URL+"/api/orders/track?code=NW-260101-0001&phone=918108398025", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (payload %v)", status, payload)
	}
}
