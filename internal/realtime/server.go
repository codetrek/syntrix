package realtime

import (
	"context"
	"log"
	"net/http"
	"strings"

	"syntrix/internal/query"
)

type Server struct {
	hub            *Hub
	queryService   query.Service
	dataCollection string
	mux            *http.ServeMux
}

func NewServer(qs query.Service, dataCollection string) *Server {
	h := NewHub()
	s := &Server{
		hub:            h,
		dataCollection: dataCollection,
		queryService:   qs,
		mux:            http.NewServeMux(),
	}
	s.mux.HandleFunc("/v1/realtime", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			ServeSSE(h, qs, w, r)
		} else {
			ServeWs(h, qs, w, r)
		}
	})
	return s
}

// StartBackgroundTasks starts the hub and the change stream watcher.
// It returns an error if watching fails to start.
// The background tasks run until ctx is cancelled.
func (s *Server) StartBackgroundTasks(ctx context.Context) error {
	go s.hub.Run(ctx)

	// Watch all collections
	stream, err := s.queryService.WatchCollection(ctx, "")
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

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
