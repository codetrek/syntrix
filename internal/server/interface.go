package server

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
)

// Service is the unified interface for the network layer.
type Service interface {
	// Start initializes and starts the HTTP and gRPC listeners.
	// It blocks until a fatal error occurs or the context is canceled.
	Start(ctx context.Context) error

	// Stop initiates a graceful shutdown of all servers.
	// It waits for active connections to drain or for the context to expire.
	Stop(ctx context.Context) error

	// RegisterHTTPHandler registers a handler for a specific pattern.
	// This must be called BEFORE Start().
	RegisterHTTPHandler(pattern string, handler http.Handler)

	// RegisterGRPCService registers a gRPC service implementation.
	// This must be called BEFORE Start().
	RegisterGRPCService(desc *grpc.ServiceDesc, impl interface{})
}
