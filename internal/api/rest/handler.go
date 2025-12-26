package rest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/query"
	"github.com/codetrek/syntrix/pkg/model"
)

type Handler struct {
	engine query.Service
	auth   identity.AuthN
	authz  identity.AuthZ
}

func NewHandler(engine query.Service, auth identity.AuthN, authz identity.AuthZ) *Handler {
	return &Handler{
		engine: engine,
		auth:   auth,
		authz:  authz,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Document Operations
	mux.HandleFunc("GET /api/v1/{path...}", h.maybeProtected(h.authorized(h.handleGetDocument, "read")))
	mux.HandleFunc("POST /api/v1/{path...}", h.maybeProtected(h.authorized(h.handleCreateDocument, "create")))
	mux.HandleFunc("PUT /api/v1/{path...}", h.maybeProtected(h.authorized(h.handleReplaceDocument, "update")))
	mux.HandleFunc("PATCH /api/v1/{path...}", h.maybeProtected(h.authorized(h.handlePatchDocument, "update")))
	mux.HandleFunc("DELETE /api/v1/{path...}", h.maybeProtected(h.authorized(h.handleDeleteDocument, "delete")))

	// Query Operations
	mux.HandleFunc("POST /api/v1/query", h.protected(h.handleQuery))

	// Replication Operations
	mux.HandleFunc("GET /replication/v1/pull", h.protected(h.handlePull))
	mux.HandleFunc("POST /replication/v1/push", h.protected(h.handlePush))

	// Trigger Internal Operations
	mux.HandleFunc("POST /trigger/v1/get", h.triggerProtected(h.handleTriggerGet))
	mux.HandleFunc("POST /trigger/v1/query", h.triggerProtected(h.handleQuery))
	mux.HandleFunc("POST /trigger/v1/write", h.triggerProtected(h.handleTriggerWrite))

	// Auth Operations
	if h.auth != nil {
		mux.HandleFunc("POST /auth/v1/signup", h.handleSignUp)
		mux.HandleFunc("POST /auth/v1/login", h.handleLogin)
		mux.HandleFunc("POST /auth/v1/refresh", h.handleRefresh)
		mux.HandleFunc("POST /auth/v1/logout", h.handleLogout)

		// Admin Operations
		mux.HandleFunc("GET /admin/users", h.adminOnly(h.handleAdminListUsers))
		mux.HandleFunc("PATCH /admin/users/{id}", h.adminOnly(h.handleAdminUpdateUser))
		mux.HandleFunc("GET /admin/rules", h.adminOnly(h.handleAdminGetRules))
		mux.HandleFunc("POST /admin/rules/push", h.adminOnly(h.handleAdminPushRules))
		mux.HandleFunc("GET /admin/health", h.adminOnly(h.handleAdminHealth))
	}

	// Health Check
	mux.HandleFunc("GET /health", h.handleHealth)
}

func (h *Handler) protected(handler http.HandlerFunc) http.HandlerFunc {
	if h.auth == nil {
		return handler
	}
	return func(w http.ResponseWriter, r *http.Request) {
		h.auth.Middleware(handler).ServeHTTP(w, r)
	}
}

func (h *Handler) maybeProtected(handler http.HandlerFunc) http.HandlerFunc {
	if h.auth == nil {
		return handler
	}
	return func(w http.ResponseWriter, r *http.Request) {
		h.auth.MiddlewareOptional(handler).ServeHTTP(w, r)
	}
}

func (h *Handler) authorized(handler http.HandlerFunc, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.authz == nil {
			handler(w, r)
			return
		}

		path := r.PathValue("path")

		// Build Request Context
		reqCtx := identity.AuthzRequest{
			Time: time.Now(),
		}

		// Extract Auth
		if uid, ok := r.Context().Value(identity.ContextKeyUserID).(string); ok {
			reqCtx.Auth.UID = uid
		}
		if username, ok := r.Context().Value(identity.ContextKeyUsername).(string); ok {
			reqCtx.Auth.Username = username
		}
		if roles, ok := r.Context().Value(identity.ContextKeyRoles).([]string); ok {
			reqCtx.Auth.Roles = append([]string{}, roles...)
		}
		if claims, ok := r.Context().Value(identity.ContextKeyClaims).(*identity.Claims); ok {
			reqCtx.Auth.Claims = claimsToMap(claims)
		}

		// Fetch Existing Resource if needed
		var existingRes *identity.Resource
		if action != "create" {
			doc, err := h.engine.GetDocument(r.Context(), path)
			if err == nil {
				data := model.Document{}
				for k, v := range doc {
					data[k] = v
				}
				data.StripProtectedFields()
				existingRes = &identity.Resource{
					Data: data,
					ID:   doc.GetID(),
				}
			} else if err != model.ErrNotFound {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}

		// For Create/Update, extract new data
		if action == "create" || action == "update" {
			bodyBytes, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			var data map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &data); err == nil {
				reqCtx.Resource = &identity.Resource{Data: data}
			}
		}

		allowed, err := h.authz.Evaluate(r.Context(), path, action, reqCtx, existingRes)
		if err != nil {
			// Log error?
			http.Error(w, "Forbidden: Rule Error", http.StatusForbidden)
			return
		}

		if !allowed {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		handler(w, r)
	}
}

func claimsToMap(claims *identity.Claims) map[string]interface{} {
	if claims == nil {
		return nil
	}

	toTime := func(nd *jwt.NumericDate) interface{} {
		if nd == nil {
			return nil
		}
		return nd.Time
	}

	return map[string]interface{}{
		"sub":      claims.Subject,
		"username": claims.Username,
		"roles":    append([]string{}, claims.Roles...),
		"disabled": claims.Disabled,
		"aud":      claims.Audience,
		"iss":      claims.Issuer,
		"jti":      claims.ID,
		"nbf":      toTime(claims.NotBefore),
		"exp":      toTime(claims.ExpiresAt),
		"iat":      toTime(claims.IssuedAt),
	}
}

func (h *Handler) triggerProtected(handler http.HandlerFunc) http.HandlerFunc {
	if h.auth == nil {
		return handler
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// First, run standard auth middleware to validate token
		h.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check roles
			roles, ok := r.Context().Value(identity.ContextKeyRoles).([]string)
			if !ok {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			isSystem := false
			for _, role := range roles {
				if role == "system" {
					isSystem = true
					break
				}
			}

			if !isSystem {
				http.Error(w, "Forbidden: System access required", http.StatusForbidden)
				return
			}

			handler(w, r)
		})).ServeHTTP(w, r)
	}
}

func (h *Handler) adminOnly(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// First, run standard auth middleware to validate token
		h.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check roles
			roles, ok := r.Context().Value(identity.ContextKeyRoles).([]string)
			if !ok {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			isAdmin := false
			for _, role := range roles {
				if role == "admin" {
					isAdmin = true
					break
				}
			}

			if !isAdmin {
				http.Error(w, "Forbidden: Admin access required", http.StatusForbidden)
				return
			}

			handler(w, r)
		})).ServeHTTP(w, r)
	}
}
