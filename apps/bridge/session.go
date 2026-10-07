package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

func contextWithRequestTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

const (
	// SessionCookieName holds the Supabase access token. The Studio never reads
	// it: it is HttpOnly, so a cross-site script cannot read the admin session
	// out of the page.
	SessionCookieName = "nw_admin_session"
	// RefreshCookieName holds the refresh token in its own cookie so the Studio
	// stays signed in past the access token's expiry without ever handling a
	// token itself.
	RefreshCookieName = "nw_admin_refresh"
	// refreshCookieMaxAge matches Supabase's default refresh token lifetime.
	refreshCookieMaxAge = 30 * 24 * 3600
)

func (s *Server) setSessionCookies(w http.ResponseWriter, access, refresh string, expiresIn int) {
	secure := s.auth.secureCookies

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    access,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   expiresIn,
	})

	maxAge := refreshCookieMaxAge
	if refresh == "" {
		// No usable refresh token: do not leave a stale one lying around.
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    refresh,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "enter your email and password")
		return
	}

	ctx, cancel := contextWithRequestTimeout(r, 20*time.Second)
	defer cancel()

	access, refresh, expiresIn, err := s.auth.signIn(ctx, email, req.Password)
	if err != nil {
		if errors.Is(err, errBadCredentials) {
			writeError(w, http.StatusUnauthorized, errBadCredentials.Error())
			return
		}
		s.log.Error("sign in failed", "error", err)
		writeError(w, http.StatusBadGateway, "could not sign you in right now")
		return
	}

	// Reject non-admins at the door instead of letting them in and then
	// failing every single request afterwards.
	identity, err := s.auth.identity(ctx, access)
	if err != nil {
		if errors.Is(err, errNotAdmin) {
			writeError(w, http.StatusForbidden, errNotAdmin.Error())
			return
		}
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	s.setSessionCookies(w, access, refresh, expiresIn)

	// Deliberately no access token in the body: it would defeat the point of
	// the HttpOnly cookie.
	writeJSON(w, http.StatusOK, map[string]any{
		"expires_in": expiresIn,
		"role":       "admin",
		"email":      identity.Email,
	})
}

func (s *Server) handleRefreshSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(RefreshCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		clearSessionCookies(w, s.auth.secureCookies)
		writeError(w, http.StatusUnauthorized, errBadToken.Error())
		return
	}

	ctx, cancel := contextWithRequestTimeout(r, 20*time.Second)
	defer cancel()

	access, refresh, expiresIn, err := s.auth.refresh(ctx, strings.TrimSpace(cookie.Value))
	if err != nil {
		clearSessionCookies(w, s.auth.secureCookies)
		writeError(w, http.StatusUnauthorized, errBadToken.Error())
		return
	}

	s.setSessionCookies(w, access, refresh, expiresIn)
	writeJSON(w, http.StatusOK, map[string]any{"expires_in": expiresIn})
}

func (s *Server) handleSignOut(w http.ResponseWriter, _ *http.Request) {
	clearSessionCookies(w, s.auth.secureCookies)
	writeJSON(w, http.StatusOK, map[string]any{"signed_out": true})
}
