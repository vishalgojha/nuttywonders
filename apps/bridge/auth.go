package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	errNoToken        = errors.New("sign in to continue")
	errNotAdmin       = errors.New("this account is not a NuttyWonders admin")
	errBadToken       = errors.New("your session has expired, please sign in again")
	errBadCredentials = errors.New("that email and password did not match")
	adminCacheTTL     = 60 * time.Second
	// Admin tokens are refreshed hourly, so the cache only ever needs a few
	// dozen live entries before a sweep is worthwhile.
	adminCacheSweepThreshold = 32
)

type adminIdentity struct {
	UserID string
	Email  string
}

// AdminAuth verifies Supabase access tokens by asking the Supabase Auth API
// who the caller is. We deliberately do not verify JWT signatures locally:
// that would mean shipping the project JWT secret, and the round trip through
// Auth also guarantees the token has not been revoked.
type AdminAuth struct {
	authURL string
	apiKey  string
	http    *http.Client
	// secureCookies mirrors whether the public site is served over TLS, so the
	// session cookies are never sent in the clear.
	secureCookies bool

	mu    sync.Mutex
	cache map[string]adminCacheEntry
}

type adminCacheEntry struct {
	identity adminIdentity
	expires  time.Time
}

func NewAdminAuth(supabaseURL, serviceKey string, secureCookies bool) *AdminAuth {
	return &AdminAuth{
		authURL:       supabaseURL + "/auth/v1",
		apiKey:        serviceKey,
		http:          &http.Client{Timeout: 10 * time.Second},
		secureCookies: secureCookies,
		cache:         map[string]adminCacheEntry{},
	}
}

type authUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	AppMetadata struct {
		Role string `json:"role"`
	} `json:"app_metadata"`
	UserMetadata struct {
		Role string `json:"role"`
	} `json:"user_metadata"`
}

func (a *AdminAuth) identity(ctx context.Context, token string) (adminIdentity, error) {
	a.mu.Lock()
	if entry, ok := a.cache[token]; ok && time.Now().Before(entry.expires) {
		a.mu.Unlock()
		return entry.identity, nil
	}
	a.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.authURL+"/user", nil)
	if err != nil {
		return adminIdentity{}, errBadToken
	}
	req.Header.Set("apikey", a.apiKey)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.http.Do(req)
	if err != nil {
		return adminIdentity{}, fmt.Errorf("verify session: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return adminIdentity{}, errBadToken
	default:
		return adminIdentity{}, fmt.Errorf("auth service returned %d", resp.StatusCode)
	}

	var user authUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return adminIdentity{}, errBadToken
	}

	// app_metadata is set by an admin/service call and cannot be edited by the
	// user themselves; user_metadata can, so it never grants access.
	if user.AppMetadata.Role != "admin" {
		return adminIdentity{}, errNotAdmin
	}

	identity := adminIdentity{UserID: user.ID, Email: user.Email}
	a.mu.Lock()
	// Every refreshed access token would otherwise add an entry that is never
	// read again, so sweep the expired ones as we go.
	if len(a.cache) > adminCacheSweepThreshold {
		for token, entry := range a.cache {
			if time.Now().After(entry.expires) {
				delete(a.cache, token)
			}
		}
	}
	a.cache[token] = adminCacheEntry{identity: identity, expires: time.Now().Add(adminCacheTTL)}
	a.mu.Unlock()
	return identity, nil
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header != "" {
		if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
			return ""
		}
		return strings.TrimSpace(header[7:])
	}
	// The Studio never reads this cookie: it is HttpOnly and set by sign in.
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

// clearSessionCookies retires both session cookies. Sending this with an
// unauthorized response stops the Studio from retrying with a token that is
// already dead.
func clearSessionCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{SessionCookieName, RefreshCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
}

// requireAdmin wraps a handler so it only runs for signed-in admins.
func (a *AdminAuth) requireAdmin(next func(http.ResponseWriter, *http.Request, adminIdentity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, errNoToken.Error())
			return
		}
		identity, err := a.identity(r.Context(), token)
		if err != nil {
			// An expired or revoked token would otherwise be retried by the
			// Studio on every poll, so retire the cookie along with the error.
			if errors.Is(err, errBadToken) {
				clearSessionCookies(w, a.secureCookies)
			}
			status := http.StatusUnauthorized
			if errors.Is(err, errNotAdmin) {
				status = http.StatusForbidden
			}
			writeError(w, status, err.Error())
			return
		}
		next(w, r, identity)
	}
}

// Granting admin rights is deliberately not exposed as an HTTP endpoint: any
// such endpoint becomes a way to mint admin accounts for whoever finds it. The
// first admin is promoted once, by hand, with the SQL snippet in README.md
// (or the Supabase dashboard's app_metadata editor).

// signIn exchanges an email and password for a Supabase access token. The
// Studio stores that token and sends it as a bearer header; the bridge then
// asks Supabase Auth who the caller is on each request.
func (a *AdminAuth) signIn(ctx context.Context, email, password string) (accessToken, refreshToken string, expiresIn int, err error) {
	body := map[string]any{
		"grant_type": "password",
		"email":      email,
		"password":   password,
	}

	raw, status, err := a.postAuth(ctx, "/token?grant_type=password", body)
	if err != nil {
		return "", "", 0, err
	}
	if status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusNotFound {
		return "", "", 0, errBadCredentials
	}
	if status != http.StatusOK {
		return "", "", 0, fmt.Errorf("sign in failed with status %d", status)
	}

	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		SupabaseUser struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", "", 0, fmt.Errorf("could not read the sign in response")
	}
	if payload.AccessToken == "" {
		return "", "", 0, errBadCredentials
	}
	return payload.AccessToken, payload.RefreshToken, payload.ExpiresIn, nil
}

// refresh turns a refresh token into a new access token. Supabase rotates
// refresh tokens on every use, so the replacement is returned as well and the
// caller has to store it or the next refresh will fail.
func (a *AdminAuth) refresh(ctx context.Context, refreshToken string) (accessToken, newRefreshToken string, expiresIn int, err error) {
	body := map[string]any{"grant_type": "refresh_token", "refresh_token": refreshToken}

	raw, status, err := a.postAuth(ctx, "/token?grant_type=refresh_token", body)
	if err != nil {
		return "", "", 0, err
	}
	if status != http.StatusOK {
		return "", "", 0, errBadToken
	}

	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", "", 0, errBadToken
	}
	if payload.AccessToken == "" {
		return "", "", 0, errBadToken
	}
	if payload.RefreshToken == "" {
		payload.RefreshToken = refreshToken
	}
	return payload.AccessToken, payload.RefreshToken, payload.ExpiresIn, nil
}

func (a *AdminAuth) postAuth(ctx context.Context, path string, body any) (string, int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.authURL+path, strings.NewReader(string(raw)))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("apikey", a.apiKey)
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	_, _ = fmt.Fprint(buf, readAllLimited(resp))
	return strings.TrimSpace(buf.String()), resp.StatusCode, nil
}

func readAllLimited(resp *http.Response) string {
	buf := new(strings.Builder)
	chunk := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
		}
		if err != nil || buf.Len() > 1<<20 {
			break
		}
	}
	return buf.String()
}
