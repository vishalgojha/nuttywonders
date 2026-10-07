package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a thin PostgREST client. The bridge is the only thing that talks
// to Postgres, and it always uses the service-role key, so RLS never gets in
// the way here and the key never leaves the server.
type Client struct {
	base string
	key  string
	http *http.Client
}

func NewSupabaseClient(baseURL, serviceKey string) *Client {
	return &Client{
		base: baseURL + "/rest/v1",
		key:  serviceKey,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

type apiError struct {
	Status  int
	Message string
	Code    string
}

func (e *apiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("supabase %d: %s (%s)", e.Status, e.Message, e.Code)
	}
	return fmt.Sprintf("supabase %d: %s", e.Status, e.Message)
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body, out any, headers ...[2]string) error {
	endpoint := c.base + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("apikey", c.key)
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("supabase request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return parseAPIError(resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func parseAPIError(status int, raw []byte) error {
	var payload struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Hint    string `json:"hint"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Message != "" {
		return &apiError{Status: status, Message: payload.Message, Code: payload.Code}
	}
	return &apiError{Status: status, Message: strings.TrimSpace(string(raw))}
}

func (c *Client) Select(ctx context.Context, table string, query url.Values, out any) error {
	return c.request(ctx, http.MethodGet, "/"+table, query, nil, out)
}

func (c *Client) Insert(ctx context.Context, table string, body any, out any) error {
	return c.request(ctx, http.MethodPost, "/"+table, nil, body, out, [2]string{"Prefer", "return=representation"})
}

func (c *Client) Upsert(ctx context.Context, table string, body any, out any) error {
	return c.request(ctx, http.MethodPost, "/"+table, nil, body, out,
		[2]string{"Prefer", "resolution=merge-duplicates,return=representation"})
}

func (c *Client) Update(ctx context.Context, table string, query url.Values, body any, out any) error {
	return c.request(ctx, http.MethodPatch, "/"+table, query, body, out, [2]string{"Prefer", "return=representation"})
}

func (c *Client) RPC(ctx context.Context, fn string, params any, out any) error {
	return c.request(ctx, http.MethodPost, "/rpc/"+fn, nil, params, out)
}

// Count returns the number of rows a query matches. It asks PostgREST for an
// exact count in the Content-Range header and fetches a single row, so counting
// a large table stays cheap and never transfers more than one row.
func (c *Client) Count(ctx context.Context, table string, query url.Values) (int, error) {
	values := url.Values{}
	for key, list := range query {
		values[key] = list
	}
	// Select the primary key only, and fetch a single row: the count arrives in
	// the header, not the body.
	values.Set("select", "id")
	values.Set("limit", "1")

	count, err := c.requestForCount(ctx, http.MethodGet, "/"+table, values)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (c *Client) requestForCount(ctx context.Context, method, path string, query url.Values) (int, error) {
	endpoint := c.base + path + "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("apikey", c.key)
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Prefer", "count=exact")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("supabase request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return 0, parseAPIError(resp.StatusCode, raw)
	}

	// Content-Range looks like "0-0/57" or "*/0" for an empty table.
	contentRange := resp.Header.Get("Content-Range")
	slash := strings.LastIndex(contentRange, "/")
	if slash < 0 {
		return 0, fmt.Errorf("no count in Content-Range header %q", contentRange)
	}
	count, err := strconv.Atoi(strings.TrimSpace(contentRange[slash+1:]))
	if err != nil {
		return 0, fmt.Errorf("unreadable count in Content-Range header %q", contentRange)
	}
	return count, nil
}

// RPCScalar calls a PostgREST function that returns a single scalar. Postgres
// returns those as a bare JSON value (a number, not an array), so a count can
// never be decoded into a Go int by the normal row path.
func (c *Client) RPCScalar(ctx context.Context, fn string, params any) (int, error) {
	var count int
	if err := c.request(ctx, http.MethodPost, "/rpc/"+fn, nil, params, &count); err != nil {
		return 0, err
	}
	return count, nil
}
