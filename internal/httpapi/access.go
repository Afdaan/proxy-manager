package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/afdaan/proxy-manager/internal/store"
)

type createManagedUserRequest struct {
	Email         string                  `json:"email"`
	DisplayName   string                  `json:"displayName"`
	Password      string                  `json:"password"`
	PlatformAdmin bool                    `json:"platformAdmin"`
	Memberships   []store.MembershipInput `json:"memberships"`
}

type updateManagedUserRequest struct {
	Email         string                  `json:"email"`
	DisplayName   string                  `json:"displayName"`
	Enabled       bool                    `json:"enabled"`
	PlatformAdmin bool                    `json:"platformAdmin"`
	Memberships   []store.MembershipInput `json:"memberships"`
	Version       int64                   `json:"version"`
}

type resetManagedUserPasswordRequest struct {
	Password string `json:"password"`
}

func (server Server) accessOverview(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	overview, err := server.Store.AccessOverview(request.Context(), auth.Session.User.ID)
	if errors.Is(err, store.ErrForbidden) {
		writeError(response, http.StatusForbidden, "permission_denied", "platform administrator permission required")
		return
	}
	if err != nil {
		server.internalError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, overview)
}

func (server Server) createManagedUser(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	var input createManagedUserRequest
	if err := decodeJSON(response, request, &input, 32<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	user, err := server.Store.CreateManagedUser(request.Context(), auth.Session.User.ID, store.CreateManagedUserParams{
		Email:         input.Email,
		DisplayName:   input.DisplayName,
		Password:      input.Password,
		PlatformAdmin: input.PlatformAdmin,
		Memberships:   input.Memberships,
		RequestID:     requestIDFromContext(request.Context()),
		SourceIP:      clientIP(request),
	})
	if server.writeAccessError(response, request, err, "email is already assigned") {
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"user": user})
}

func (server Server) updateManagedUser(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	var input updateManagedUserRequest
	if err := decodeJSON(response, request, &input, 32<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	user, err := server.Store.UpdateManagedUser(request.Context(), auth.Session.User.ID, store.UpdateManagedUserParams{
		ID:            strings.TrimSpace(request.PathValue("userID")),
		Email:         input.Email,
		DisplayName:   input.DisplayName,
		Enabled:       input.Enabled,
		PlatformAdmin: input.PlatformAdmin,
		Memberships:   input.Memberships,
		Version:       input.Version,
		RequestID:     requestIDFromContext(request.Context()),
		SourceIP:      clientIP(request),
	})
	if server.writeAccessError(response, request, err, "email is already assigned") {
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"user": user})
}

func (server Server) resetManagedUserPassword(response http.ResponseWriter, request *http.Request) {
	auth := request.Context().Value(authContextKey{}).(authContext)
	var input resetManagedUserPasswordRequest
	if err := decodeJSON(response, request, &input, 8<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	err := server.Store.ResetManagedUserPassword(
		request.Context(),
		auth.Session.User.ID,
		strings.TrimSpace(request.PathValue("userID")),
		input.Password,
		requestIDFromContext(request.Context()),
		clientIP(request),
	)
	if server.writeAccessError(response, request, err, "request conflicts with the current account state") {
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server Server) writeAccessError(response http.ResponseWriter, request *http.Request, err error, conflictMessage string) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrForbidden):
		writeError(response, http.StatusForbidden, "permission_denied", "platform administrator permission required")
	case errors.Is(err, store.ErrNotFound):
		writeError(response, http.StatusNotFound, "user_not_found", "user not found")
	case errors.Is(err, store.ErrStaleVersion):
		writeError(response, http.StatusConflict, "stale_version", "user changed; reload before saving")
	case errors.Is(err, store.ErrLastAdministrator):
		writeError(response, http.StatusConflict, "last_administrator", err.Error())
	case errors.Is(err, store.ErrSelfAccessChange):
		writeError(response, http.StatusConflict, "self_access_change", err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(response, http.StatusConflict, "user_conflict", conflictMessage)
	case isAccessValidationError(err):
		writeError(response, http.StatusBadRequest, "invalid_user", err.Error())
	default:
		server.internalError(response, request, err)
	}
	return true
}

func isAccessValidationError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "must ") ||
		strings.Contains(message, "invalid ") ||
		strings.Contains(message, "required") ||
		strings.Contains(message, "does not exist") ||
		strings.Contains(message, "only one role")
}
