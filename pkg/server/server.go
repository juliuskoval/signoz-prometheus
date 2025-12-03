package server

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

type Server struct {
	signozBaseURL string
	httpClient    *http.Client
	r             *mux.Router
}

func BuildServer() *Server {
	signozBaseURL := "http://signoz:8080"
	if endpoint := os.Getenv("SIGNOZ_URL"); endpoint != "" {
		if _, err := url.ParseRequestURI(endpoint); err != nil {
			zap.L().Fatal("Invalid endpoint", zap.String("server.address", endpoint), zap.Error(err))
		}
		signozBaseURL = endpoint
		zap.L().Info("Setting SigNoz API endpoint", zap.String("server.address", endpoint))
	} else {
		zap.L().Info("Using the default SigNoz endpoint", zap.String("server.address", signozBaseURL))
	}

	tlsCfg, err := buildTLSConfig()
	if err != nil {
		zap.L().Fatal("Failed to build TLS config", zap.Error(err))
	}

	tr := &http.Transport{TLSClientConfig: tlsCfg}

	server := &Server{
		signozBaseURL: signozBaseURL,
		httpClient:    &http.Client{Transport: tr, Timeout: 30 * time.Second},
		r:             mux.NewRouter(),
	}

	return server
}

func buildTLSConfig() (*tls.Config, error) { // TODO
	return &tls.Config{InsecureSkipVerify: true}, nil
}

func (s *Server) RegisterRoutes() {
	s.r.HandleFunc("/api/v1/query", s.getQuery)
	s.r.HandleFunc("/api/v1/query_range", s.getQueryRange)
	s.r.HandleFunc("/api/v1/labels", s.getLabels)
	s.r.HandleFunc("/api/v1/label/{label}/values", s.getLabelValues)
	s.r.HandleFunc("/", s.handleFallback)
}

func (s *Server) Start() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	addr := ":" + port

	srv := &http.Server{
		Addr:    addr,
		Handler: s.r,
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		zap.L().Info("Shutdown signal received, draining connections")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			zap.L().Error("Error during server shutdown", zap.Error(err))
		}
		close(idleConnsClosed)
	}()

	zap.L().Info("Starting server", zap.String("server.address", addr))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		zap.L().Fatal("Could not start server", zap.Error(err))
	}

	<-idleConnsClosed
	zap.L().Info("Server stopped")
}
