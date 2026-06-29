package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
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
		// Wrap the transport so each outbound call to SigNoz becomes a client
		// span and injects W3C trace-context headers for downstream correlation.
		httpClient: &http.Client{Transport: otelhttp.NewTransport(tr), Timeout: 30 * time.Second},
		r:          mux.NewRouter(),
	}

	return server
}

func buildTLSConfig() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if os.Getenv("SIGNOZ_TLS_SKIP_VERIFY") == "true" {
		zap.L().Warn("TLS certificate verification disabled (SIGNOZ_TLS_SKIP_VERIFY=true)")
		cfg.InsecureSkipVerify = true
	}

	if caPath := os.Getenv("SIGNOZ_TLS_CA_CERT"); caPath != "" {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("reading CA bundle %q: %w", caPath, err)
		}
		// Start from the system roots so public CAs still validate, then add the
		// private CA. SystemCertPool can fail on some platforms, so fall back to
		// an empty pool holding only our CA.
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid certificates found in CA bundle %q", caPath)
		}
		cfg.RootCAs = pool
		zap.L().Info("Loaded custom CA bundle for SigNoz TLS", zap.String("path", caPath))
	}

	return cfg, nil
}

func (s *Server) RegisterRoutes() {
	// otelmux starts a server span per request, named by the matched route
	// template so concrete path values (e.g. label names) don't explode span
	// cardinality.
	s.r.Use(otelmux.Middleware("signoz-prometheus"))

	s.r.HandleFunc("/healthz", s.getHealth)
	s.r.HandleFunc("/api/v1/query", s.getQuery)
	s.r.HandleFunc("/api/v1/query_range", s.getQueryRange)
	s.r.HandleFunc("/api/v1/labels", s.getLabels)
	s.r.HandleFunc("/api/v1/label/{label}/values", s.getLabelValues)
	s.r.HandleFunc("/api/v1/metadata", s.getMetadata)

	s.r.NotFoundHandler = http.HandlerFunc(s.handleFallback)
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
