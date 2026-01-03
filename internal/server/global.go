package server

import (
	"log/slog"
	"net/http"

	"google.golang.org/grpc"
)

var (
	defaultService Service
)

// InitDefault initializes the global default server instance.
// This should be called once at application startup.
func InitDefault(cfg Config, logger *slog.Logger) {
	defaultService = New(cfg, logger)
}

// Default returns the global default server instance.
// It returns nil if InitDefault has not been called.
func Default() Service {
	return defaultService
}

// RegisterHTTP registers an HTTP handler with the default server.
func RegisterHTTP(pattern string, handler http.Handler) {
	if s := Default(); s != nil {
		s.RegisterHTTPHandler(pattern, handler)
	}
}

// HandleFunc registers an HTTP handler function with the default server.
func HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	if s := Default(); s != nil {
		s.RegisterHTTPHandler(pattern, http.HandlerFunc(handler))
	}
}

// RegisterGRPC registers a gRPC service with the default server.
func RegisterGRPC(desc *grpc.ServiceDesc, impl interface{}) {
	if s := Default(); s != nil {
		s.RegisterGRPCService(desc, impl)
	}
}
