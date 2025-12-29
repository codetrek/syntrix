package api

import (
	"net/http"

	"github.com/codetrek/syntrix/internal/api/realtime"
	"github.com/codetrek/syntrix/internal/api/rest"
	"github.com/codetrek/syntrix/internal/engine"
	"github.com/codetrek/syntrix/internal/identity"
)

type Server struct {
	mux      *http.ServeMux
	rest     *rest.Handler
	realtime *realtime.Server
}

func NewServer(engine engine.Service, auth identity.AuthN, authz identity.AuthZ, rt *realtime.Server) *Server {
	s := &Server{
		mux:      http.NewServeMux(),
		rest:     rest.NewHandler(engine, auth, authz),
		realtime: rt,
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
	// Register REST routes
	s.rest.RegisterRoutes(s.mux)

	// Register Realtime routes
	if s.realtime != nil {
		s.mux.HandleFunc("GET /realtime/ws", s.realtime.HandleWS)
		s.mux.HandleFunc("GET /realtime/sse", s.realtime.HandleSSE)
	}

	// Console
	s.mux.Handle("GET /console/", http.StripPrefix("/console/", http.FileServer(http.Dir("console"))))
}
