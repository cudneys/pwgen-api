package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/cudneys/pwgen-api/internal/cors"
	"github.com/cudneys/pwgen-api/internal/httpapi"
	"github.com/cudneys/pwgen-api/internal/logging"
	"github.com/cudneys/pwgen-api/internal/passwordgen"
	"github.com/cudneys/pwgen-api/internal/telemetry"
)

const serviceName = "pwgen-api"

// version is the build version, overridden at build time via
// -ldflags "-X main.version=<v>". It is reported as the service.version
// attribute on all telemetry.
var version = "dev"

func main() {
	logger := logging.Setup()

	routerService := os.Getenv("ROUTER_SERVICE")
	if routerService == "" {
		logger.Error("ROUTER_SERVICE environment variable is required")
		os.Exit(1)
	}

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	ctx := context.Background()

	tp, err := telemetry.Setup(ctx, serviceName, version)
	if err != nil {
		logger.Error("failed to initialise telemetry", slog.Any("error", err))
		os.Exit(1)
	}

	gen := passwordgen.New(routerService)
	handler, err := httpapi.NewHandler(gen, logger)
	if err != nil {
		logger.Error("failed to build handler", slog.Any("error", err))
		os.Exit(1)
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	// CORS: enabled only when CORS_ALLOWED_ORIGINS is set (comma-separated list
	// of origins, or "*" for any). Registered before otelgin so preflight
	// OPTIONS requests short-circuit without producing a trace span.
	corsOrigins := cors.ParseOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if len(corsOrigins) > 0 {
		logger.Info("CORS enabled", slog.Any("allowed_origins", corsOrigins))
		if bad := cors.Suspicious(corsOrigins); len(bad) > 0 {
			logger.Warn("CORS origins missing a scheme will never match a browser Origin header; use e.g. https://host",
				slog.Any("origins", bad))
		}
	}
	router.Use(cors.Middleware(corsOrigins))
	// otelgin extracts incoming trace headers and starts a server span per request.
	// Skip /healthz so liveness/readiness probes don't flood the trace backend.
	router.Use(otelgin.Middleware(serviceName, otelgin.WithFilter(func(r *http.Request) bool {
		return r.URL.Path != "/healthz"
	})))

	router.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	handler.Register(router)

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("starting server",
			slog.String("addr", listenAddr),
			slog.String("router_service", routerService))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", slog.Any("error", err))
	}
	if err := tp.Shutdown(shutdownCtx); err != nil {
		logger.Error("telemetry shutdown error", slog.Any("error", err))
	}
}
