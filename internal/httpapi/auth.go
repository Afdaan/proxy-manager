package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/afdaan/proxy-manager/internal/store"
)

const (
	csrfCookieName    = "proxy_manager_csrf"
	sessionCookieName = "proxy_manager_session"
)

type authContext struct {
	CSRFHash     []byte
	Session      store.Session
	SessionToken string
}

type authContextKey struct{}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (server Server) login(response http.ResponseWriter, request *http.Request) {
	var input loginRequest
	if err := decodeJSON(response, request, &input, 8<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	session, sessionToken, csrfToken, err := server.Store.Login(request.Context(), input.Email, input.Password, 12*time.Hour)
	if errors.Is(err, store.ErrInvalidCredentials) {
		writeError(response, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	server.setAuthCookies(response, sessionToken, csrfToken, session.ExpiresAt)
	writeJSON(response, http.StatusOK, map[string]any{"user": session.User})
}

func (server Server) me(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	writeJSON(response, http.StatusOK, map[string]any{"user": auth.Session.User})
}

func (server Server) logout(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	if err := server.Store.Logout(request.Context(), auth.SessionToken); err != nil {
		server.internalError(response, request, err)
		return
	}
	server.clearAuthCookies(response)
	response.WriteHeader(http.StatusNoContent)
}

func (server Server) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil {
			writeError(response, http.StatusUnauthorized, "authentication_required", "sign in required")
			return
		}
		session, csrfHash, err := server.Store.Authenticate(request.Context(), cookie.Value)
		if errors.Is(err, store.ErrInvalidCredentials) {
			server.clearAuthCookies(response)
			writeError(response, http.StatusUnauthorized, "authentication_required", "sign in required")
			return
		}
		if err != nil {
			server.internalError(response, request, err)
			return
		}
		auth := authContext{Session: session, CSRFHash: csrfHash, SessionToken: cookie.Value}
		next(response, request.WithContext(context.WithValue(request.Context(), authContextKey{}, auth)))
	}
}

func (server Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		auth := request.Context().Value(authContextKey{}).(authContext)
		if !store.VerifyCSRF(request.Header.Get("X-CSRF-Token"), auth.CSRFHash) {
			writeError(response, http.StatusForbidden, "invalid_csrf_token", "request verification failed")
			return
		}
		next(response, request)
	}
}

func (server Server) setAuthCookies(response http.ResponseWriter, sessionToken, csrfToken string, expiresAt time.Time) {
	common := http.Cookie{
		Path:     "/",
		Secure:   server.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	}
	sessionCookie := common
	sessionCookie.Name = sessionCookieName
	sessionCookie.Value = sessionToken
	sessionCookie.HttpOnly = true
	http.SetCookie(response, &sessionCookie)

	csrfCookie := common
	csrfCookie.Name = csrfCookieName
	csrfCookie.Value = csrfToken
	http.SetCookie(response, &csrfCookie)
}

func (server Server) clearAuthCookies(response http.ResponseWriter) {
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		http.SetCookie(response, &http.Cookie{
			Name:     name,
			Path:     "/",
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
			HttpOnly: name == sessionCookieName,
			Secure:   server.CookieSecure,
			SameSite: http.SameSiteStrictMode,
		})
	}
}
