package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/codetrek/syntrix/internal/identity"
)

func (h *Handler) handleSignUp(w http.ResponseWriter, r *http.Request) {
	var req identity.SignupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	tokenPair, err := h.auth.SignUp(r.Context(), req)
	if err != nil {
		if errors.Is(err, identity.ErrTenantRequired) {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Tenant is required")
			return
		}
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Signup failed")
		return
	}

	writeJSON(w, http.StatusOK, tokenPair)
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req identity.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	tokenPair, err := h.auth.SignIn(r.Context(), req)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid credentials")
			return
		}
		if errors.Is(err, identity.ErrAccountDisabled) {
			writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Account is disabled")
			return
		}
		if errors.Is(err, identity.ErrAccountLocked) {
			writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Account is locked")
			return
		}
		if errors.Is(err, identity.ErrTenantRequired) {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Tenant is required")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrCodeInternalError, "Login failed")
		return
	}

	writeJSON(w, http.StatusOK, tokenPair)
}

func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req identity.RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	tokenPair, err := h.auth.Refresh(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid or expired refresh token")
		return
	}

	writeJSON(w, http.StatusOK, tokenPair)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Can accept refresh token in body or Authorization header
	var refreshToken string

	// Try body first
	var req identity.RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.RefreshToken != "" {
		refreshToken = req.RefreshToken
	} else {
		// Try Authorization header
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			refreshToken = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if refreshToken == "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Missing refresh token")
		return
	}

	if err := h.auth.Logout(r.Context(), refreshToken); err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternalError, "Logout failed")
		return
	}

	w.WriteHeader(http.StatusOK)
}
