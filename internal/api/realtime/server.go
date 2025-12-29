package realtime

import (
	"context"
	"log"
	"net/http"

	"github.com/codetrek/syntrix/internal/engine"
	"github.com/codetrek/syntrix/internal/identity"
)

type Server struct {
	hub            *Hub
	queryService   engine.Service
	dataCollection string
	auth           identity.AuthN
	cfg            Config
}

// Config controls realtime auth and CORS behavior.
type Config struct {
	AllowedOrigins []string
	AllowDevOrigin bool
	EnableAuth     bool
}

func NewServer(qs engine.Service, dataCollection string, auth identity.AuthN, cfg Config) *Server {
	h := NewHub()
	s := &Server{
		hub:            h,
		dataCollection: dataCollection,
		queryService:   qs,
		auth:           auth,
		cfg:            cfg,
	}
	return s
}

func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	s.wrapWS(w, r)
}

func (s *Server) HandleSSE(w http.ResponseWriter, r *http.Request) {
	s.wrapSSE(w, r)
}

func (s *Server) wrapWS(w http.ResponseWriter, r *http.Request) {
	if tokenFromQueryParam(r) != "" {
		http.Error(w, "Query token not allowed", http.StatusUnauthorized)
		return
	}

	if s.cfg.EnableAuth && s.auth != nil {
		s.auth.MiddlewareOptional(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ServeWs(s.hub, s.queryService, s.auth, s.cfg, w, r)
		})).ServeHTTP(w, r)
		return
	}

	ServeWs(s.hub, s.queryService, s.auth, s.cfg, w, r)
}

func (s *Server) wrapSSE(w http.ResponseWriter, r *http.Request) {
	if s.cfg.EnableAuth && s.auth != nil {
		s.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ServeSSE(s.hub, s.queryService, s.auth, s.cfg, w, r)
		})).ServeHTTP(w, r)
		return
	}

	ServeSSE(s.hub, s.queryService, s.auth, s.cfg, w, r)
}

func tokenFromQueryParam(r *http.Request) string {
	if r == nil {
		return ""
	}
	q := r.URL.Query()
	if v := q.Get("access_token"); v != "" {
		return v
	}
	if v := q.Get("token"); v != "" {
		return v
	}
	return ""
}

// StartBackgroundTasks starts the hub and the change stream watcher.
// It returns an error if watching fails to start.
// The background tasks run until ctx is cancelled.
func (s *Server) StartBackgroundTasks(ctx context.Context) error {
	go s.hub.Run(ctx)

	// Watch all collections
	stream, err := s.queryService.WatchCollection(ctx, "", "")
	if err != nil {
		return err
	}

	go func() {
		log.Println("[Realtime] Started watching change stream")
		for {
			select {
			case <-ctx.Done():
				log.Println("[Realtime] Context cancelled, stopping background tasks")
				return
			case evt, ok := <-stream:
				if !ok {
					log.Println("[Realtime] Change stream closed")
					return
				}
				// Broadcast all events, let Hub filter by subscription
				log.Printf("[Realtime] Broadcasting event type=%s id=%s", evt.Type, evt.Id)
				s.hub.Broadcast(evt)
			}
		}
	}()

	return nil
}
