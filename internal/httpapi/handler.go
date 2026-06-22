// Package httpapi exposes the password generation REST endpoint.
package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/cudneys/pwgen-api/internal/passwordgen"
)

// maxLength caps the requested password length to protect the backend.
const maxLength = 4096

// Handler serves the password endpoints.
type Handler struct {
	gen    *passwordgen.Generator
	logger *slog.Logger

	requests metric.Int64Counter
	failures metric.Int64Counter
	duration metric.Float64Histogram
}

// NewHandler builds a Handler backed by the given generator, registering its
// custom metrics with the global (Prometheus-backed) meter provider.
func NewHandler(gen *passwordgen.Generator, logger *slog.Logger) (*Handler, error) {
	meter := otel.Meter("github.com/cudneys/pwgen-api")

	requests, err := meter.Int64Counter("password_requests_total",
		metric.WithDescription("Total number of password generation requests"))
	if err != nil {
		return nil, err
	}
	failures, err := meter.Int64Counter("password_request_failures_total",
		metric.WithDescription("Total number of failed password generation requests"))
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram("password_generation_duration_seconds",
		metric.WithDescription("Time taken to generate a password"),
		metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}

	return &Handler{
		gen:      gen,
		logger:   logger,
		requests: requests,
		failures: failures,
		duration: duration,
	}, nil
}

// Register mounts the routes onto the router.
func (h *Handler) Register(r gin.IRoutes) {
	r.GET("/password/:length", h.generatePassword)
}

func (h *Handler) generatePassword(c *gin.Context) {
	ctx := c.Request.Context()
	h.requests.Add(ctx, 1)

	raw := c.Param("length")
	length, err := strconv.Atoi(raw)
	if err != nil || length <= 0 {
		h.failures.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "invalid_length")))
		h.logger.WarnContext(ctx, "invalid length parameter", slog.String("length", raw))
		c.JSON(http.StatusBadRequest, gin.H{"error": "length must be a positive integer"})
		return
	}
	if length > maxLength {
		h.failures.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "length_too_large")))
		h.logger.WarnContext(ctx, "length exceeds maximum", slog.Int("length", length), slog.Int("max", maxLength))
		c.JSON(http.StatusBadRequest, gin.H{"error": "length exceeds maximum", "max": maxLength})
		return
	}

	start := time.Now()
	password, err := h.gen.Generate(ctx, length)
	elapsed := time.Since(start).Seconds()
	h.duration.Record(ctx, elapsed, metric.WithAttributes(attribute.Int("length", length)))

	if err != nil {
		h.failures.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "backend_error")))
		h.logger.ErrorContext(ctx, "password generation failed",
			slog.Int("length", length), slog.Any("error", err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to generate password"})
		return
	}

	h.logger.InfoContext(ctx, "password generated",
		slog.Int("length", length), slog.Float64("duration_seconds", elapsed))
	c.JSON(http.StatusOK, gin.H{"password": password, "length": length})
}
