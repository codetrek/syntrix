package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"syntrix/internal/authz"
	"syntrix/internal/query"
	"syntrix/internal/storage"
	"time"
)

type Server struct {
	engine query.Service
	auth   AuthService
	authz  AuthzService
	mux    *http.ServeMux
}

func NewServer(engine query.Service, auth AuthService, authz AuthzService) *Server {
	s := &Server{
		engine: engine,
		auth:   auth,
		authz:  authz,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	// Console
	s.mux.Handle("GET /console/", http.StripPrefix("/console/", http.FileServer(http.Dir("console"))))

	// Document Operations
	s.mux.HandleFunc("GET /v1/{path...}", s.maybeProtected(s.authorized(s.handleGetDocument, "read")))
	s.mux.HandleFunc("POST /v1/{path...}", s.maybeProtected(s.authorized(s.handleCreateDocument, "create")))
	s.mux.HandleFunc("PUT /v1/{path...}", s.maybeProtected(s.authorized(s.handleReplaceDocument, "update")))
	s.mux.HandleFunc("PATCH /v1/{path...}", s.maybeProtected(s.authorized(s.handlePatchDocument, "update")))
	s.mux.HandleFunc("DELETE /v1/{path...}", s.maybeProtected(s.authorized(s.handleDeleteDocument, "delete")))

	// Query Operations
	s.mux.HandleFunc("POST /v1/query", s.protected(s.handleQuery))

	// Replication Operations
	s.mux.HandleFunc("GET /v1/replication/pull", s.protected(s.handlePull))
	s.mux.HandleFunc("POST /v1/replication/push", s.protected(s.handlePush))

	// Trigger Internal Operations
	s.mux.HandleFunc("POST /v1/trigger/get", s.triggerProtected(s.handleTriggerGet))
	s.mux.HandleFunc("POST /v1/trigger/query", s.triggerProtected(s.handleQuery))
	s.mux.HandleFunc("POST /v1/trigger/write", s.triggerProtected(s.handleTriggerWrite))

	// Auth Operations
	if s.auth != nil {
		s.mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
		s.mux.HandleFunc("POST /v1/auth/refresh", s.handleRefresh)
		s.mux.HandleFunc("POST /v1/auth/logout", s.handleLogout)

		// Admin Operations
		s.mux.HandleFunc("GET /admin/users", s.adminOnly(s.handleAdminListUsers))
		s.mux.HandleFunc("PATCH /admin/users/{id}", s.adminOnly(s.handleAdminUpdateUser))
		s.mux.HandleFunc("GET /admin/rules", s.adminOnly(s.handleAdminGetRules))
		s.mux.HandleFunc("POST /admin/rules/push", s.adminOnly(s.handleAdminPushRules))
		s.mux.HandleFunc("GET /admin/health", s.adminOnly(s.handleAdminHealth))
	}

	// Health Check
	s.mux.HandleFunc("GET /health", s.handleHealth)
}

func (s *Server) protected(h http.HandlerFunc) http.HandlerFunc {
	if s.auth == nil {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		s.auth.Middleware(h).ServeHTTP(w, r)
	}
}

func (s *Server) maybeProtected(h http.HandlerFunc) http.HandlerFunc {
	if s.auth == nil {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		s.auth.MiddlewareOptional(h).ServeHTTP(w, r)
	}
}

func (s *Server) authorized(h http.HandlerFunc, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authz == nil {
			h(w, r)
			return
		}

		path := r.PathValue("path")

		// Build Request Context
		reqCtx := authz.Request{
			Time: time.Now(),
		}

		// Extract Auth
		if uid, ok := r.Context().Value("userID").(string); ok {
			reqCtx.Auth.UID = uid
		}
		reqCtx.Auth.Token = map[string]interface{}{
			"uid": reqCtx.Auth.UID,
		}
		if roles, ok := r.Context().Value("roles").([]string); ok {
			rInterface := make([]interface{}, len(roles))
			for i, v := range roles {
				rInterface[i] = v
			}
			reqCtx.Auth.Token["roles"] = rInterface
		}

		// Fetch Existing Resource if needed
		var existingRes *authz.Resource
		if action != "create" {
			doc, err := s.engine.GetDocument(r.Context(), path)
			if err == nil {
				existingRes = &authz.Resource{
					Data: doc.Data,
					ID:   doc.Id,
				}
			} else if err != storage.ErrNotFound {
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
				reqCtx.Resource = &authz.Resource{Data: data}
			}
		}

		allowed, err := s.authz.Evaluate(r.Context(), path, action, reqCtx, existingRes)
		if err != nil {
			// Log error?
			http.Error(w, "Forbidden: Rule Error", http.StatusForbidden)
			return
		}

		if !allowed {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		h(w, r)
	}
}

func (s *Server) triggerProtected(h http.HandlerFunc) http.HandlerFunc {
	if s.auth == nil {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// First, run standard auth middleware to validate token
		s.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check roles
			roles, ok := r.Context().Value("roles").([]string)
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

			h(w, r)
		})).ServeHTTP(w, r)
	}
}
