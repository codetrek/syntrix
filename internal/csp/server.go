package csp

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/codetrek/syntrix/internal/storage"
)

type Server struct {
	mux     *http.ServeMux
	storage storage.DocumentStore
}

func NewServer(storage storage.DocumentStore) *Server {
	s := &Server{
		mux:     http.NewServeMux(),
		storage: storage,
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/internal/v1/watch", s.handleWatch)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("CSP Service OK"))
}

func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Collection string `json:"collection"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Println("[Error][Watch] invalid request body:", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Ensure the writer supports flushing
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Println("[Error][Watch] streaming not supported")
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Flush headers immediately
	flusher.Flush()

	// Start watching via Storage Backend (Mongo)
	stream, err := s.storage.Watch(r.Context(), req.Collection, nil, storage.WatchOptions{})
	if err != nil {
		// Headers already sent, can't send error status now.
		log.Println("[Error][Watch] failed to start watch:", err)
		return
	}

	log.Printf("[Info][Watch] starting watch on collection: %s", req.Collection)

	encoder := json.NewEncoder(w)

	for {
		select {
		case <-r.Context().Done():
			log.Println("[Info][Watch] context cancelled, stopping watch")
			return
		case evt, ok := <-stream:
			if !ok {
				log.Println("[Info][Watch] watch stream closed")
				return
			}
			if err := encoder.Encode(evt); err != nil {
				log.Println("[Error][Watch] failed to encode event:", err)
				return
			}
			log.Println("[Trace][Watch] sending event:", evt)
			flusher.Flush()
		}
	}
}
