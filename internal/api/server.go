package api

import (
	"net/http"
	"syntrix/internal/auth"
	"syntrix/internal/query"
)

type Server struct {
	engine query.Service
	auth   *auth.AuthService
	mux    *http.ServeMux
}

func NewServer(engine query.Service, auth *auth.AuthService) *Server {
	s := &Server{
		engine: engine,
		auth:   auth,
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
	// Document Operations
	s.mux.HandleFunc("GET /v1/{path...}", s.protected(s.handleGetDocument))
	s.mux.HandleFunc("POST /v1/{path...}", s.protected(s.handleCreateDocument))
	s.mux.HandleFunc("PUT /v1/{path...}", s.protected(s.handleReplaceDocument))
	s.mux.HandleFunc("PATCH /v1/{path...}", s.protected(s.handleUpdateDocument))
	s.mux.HandleFunc("DELETE /v1/{path...}", s.protected(s.handleDeleteDocument))

	// Query Operations
	s.mux.HandleFunc("POST /v1/query", s.protected(s.handleQuery))

	// Replication Operations
	s.mux.HandleFunc("GET /v1/replication/pull", s.protected(s.handlePull))
	s.mux.HandleFunc("POST /v1/replication/push", s.protected(s.handlePush))

	// Auth Operations
	if s.auth != nil {
		s.mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
		s.mux.HandleFunc("POST /v1/auth/refresh", s.handleRefresh)
		s.mux.HandleFunc("POST /v1/auth/logout", s.handleLogout)
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
