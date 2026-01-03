package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Context keys for request-scoped values
type contextKey string

const (
	contextKeyRequestID contextKey = "request_id"
)

// APIError represents a structured error response
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError writes a structured JSON error response
func writeError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(APIError{Code: code, Message: message}); err != nil {
		slog.Warn("Failed to encode error response", "error", err)
	}
}

// GetRequestID retrieves the request ID from the context
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(contextKeyRequestID).(string); ok {
		return id
	}
	return ""
}

// --- HTTP Middleware ---

// Middleware defines a function that wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares in reverse order.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func (s *serverImpl) wrapMiddleware(h http.Handler) http.Handler {
	mws := []Middleware{
		s.recoveryMiddleware,
		s.requestIDMiddleware,
		s.loggingMiddleware,
	}
	if s.cfg.EnableCORS {
		mws = append(mws, s.corsMiddleware)
	}
	return Chain(h, mws...)
}

func (s *serverImpl) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.logger.Error("Panic recovered",
					"method", r.Method,
					"path", r.URL.Path,
					"error", err,
					"stack", string(debug.Stack()),
					"request_id", GetRequestID(r.Context()),
				)

				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *serverImpl) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", reqID)
		ctx := context.WithValue(r.Context(), contextKeyRequestID, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *serverImpl) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(ww, r)

		duration := time.Since(start)
		reqID := GetRequestID(r.Context())

		level := slog.LevelInfo
		if ww.statusCode >= 500 {
			level = slog.LevelError
		}

		s.logger.Log(r.Context(), level, "HTTP Request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.statusCode,
			"duration_ms", duration.Milliseconds(),
			"request_id", reqID,
			"ip", r.RemoteAddr,
		)
	})
}

// TimeoutMiddleware wraps a handler with a context timeout
func TimeoutMiddleware(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (s *serverImpl) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// --- gRPC Interceptors ---

func (s *serverImpl) unaryInterceptors() grpc.ServerOption {
	interceptors := []grpc.UnaryServerInterceptor{
		s.recoveryUnaryInterceptor,
		s.loggingUnaryInterceptor,
	}
	return grpc.ChainUnaryInterceptor(interceptors...)
}

func (s *serverImpl) streamInterceptors() grpc.ServerOption {
	interceptors := []grpc.StreamServerInterceptor{
		s.recoveryStreamInterceptor,
		s.loggingStreamInterceptor,
	}
	return grpc.ChainStreamInterceptor(interceptors...)
}

func (s *serverImpl) recoveryUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("gRPC panic recovered",
				"method", info.FullMethod,
				"error", r,
				"stack", string(debug.Stack()),
			)
			err = status.Errorf(codes.Internal, "Internal server error")
		}
	}()
	return handler(ctx, req)
}

func (s *serverImpl) recoveryStreamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("gRPC panic recovered",
				"method", info.FullMethod,
				"error", r,
				"stack", string(debug.Stack()),
			)
			err = status.Errorf(codes.Internal, "Internal server error")
		}
	}()
	return handler(srv, ss)
}

func (s *serverImpl) loggingUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start)

	code := status.Code(err)
	level := slog.LevelInfo
	if code != codes.OK {
		level = slog.LevelError
	}

	s.logger.Log(ctx, level, "gRPC Request",
		"method", info.FullMethod,
		"code", code,
		"duration", duration,
		"error", err,
	)

	return resp, err
}

func (s *serverImpl) loggingStreamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	err := handler(srv, ss)
	duration := time.Since(start)

	code := status.Code(err)
	level := slog.LevelInfo
	if code != codes.OK {
		level = slog.LevelError
	}

	s.logger.Log(ss.Context(), level, "gRPC Stream",
		"method", info.FullMethod,
		"code", code,
		"duration", duration,
		"error", err,
	)

	return err
}
