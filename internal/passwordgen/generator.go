// Package passwordgen builds passwords by fanning out one request per character
// to a backend "router" service, then shuffling the assembled characters.
package passwordgen

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

const (
	// maxConcurrency bounds how many backend calls are in flight at once so a
	// large length cannot exhaust sockets or overwhelm the backend.
	maxConcurrency = 32
	tracerName     = "github.com/cudneys/pwgen-api/passwordgen"
)

// Generator produces passwords using a backend character source.
type Generator struct {
	routerURL string
	client    *http.Client
}

// New returns a Generator that sources characters from routerURL. The HTTP
// client is instrumented with otelhttp so trace headers are injected into every
// outbound request automatically.
func New(routerURL string) *Generator {
	return &Generator{
		routerURL: strings.TrimRight(routerURL, "/"),
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// Generate builds a password of exactly length characters. It issues length
// concurrent (bounded) requests to the backend, collects one character from
// each, then shuffles the result a random number of times between 2 and 10.
func (g *Generator) Generate(ctx context.Context, length int) (string, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "passwordgen.Generate")
	defer span.End()
	span.SetAttributes(attribute.Int("password.length", length))

	chars := make([]rune, length)

	grp, grpCtx := errgroup.WithContext(ctx)
	grp.SetLimit(maxConcurrency)
	for i := 0; i < length; i++ {
		i := i
		grp.Go(func() error {
			r, err := g.fetchChar(grpCtx)
			if err != nil {
				return fmt.Errorf("fetch char %d: %w", i, err)
			}
			chars[i] = r
			return nil
		})
	}
	if err := grp.Wait(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "backend fetch failed")
		return "", err
	}

	if err := shuffle(chars); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "shuffle failed")
		return "", err
	}

	return string(chars), nil
}

// fetchChar performs a single backend request and returns its first rune. The
// span created here is a child of the caller's span, and otelhttp propagates the
// trace headers to the backend service.
func (g *Generator) fetchChar(ctx context.Context) (rune, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "passwordgen.fetchChar")
	defer span.End()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.routerURL, nil)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}

	resp, err := g.client.Do(req)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		span.RecordError(err)
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("backend returned status %d", resp.StatusCode)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	// The router responds with a JSON object: {"character":"b"}.
	var payload struct {
		Character string `json:"character"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		err := fmt.Errorf("decode backend response: %w", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	s := strings.TrimSpace(payload.Character)
	if s == "" {
		err := fmt.Errorf("backend returned empty character")
		span.RecordError(err)
		return 0, err
	}

	r, _ := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		err := fmt.Errorf("backend returned invalid character")
		span.RecordError(err)
		return 0, err
	}
	return r, nil
}

// shuffle performs a cryptographically-seeded Fisher-Yates shuffle a random
// number of times between 2 and 10 (inclusive).
func shuffle(chars []rune) error {
	rounds, err := randInt(2, 10)
	if err != nil {
		return err
	}
	for round := 0; round < rounds; round++ {
		for i := len(chars) - 1; i > 0; i-- {
			j, err := randInt(0, i)
			if err != nil {
				return err
			}
			chars[i], chars[j] = chars[j], chars[i]
		}
	}
	return nil
}

// randInt returns a uniform random int in [min, max] using crypto/rand.
func randInt(min, max int) (int, error) {
	if max < min {
		min, max = max, min
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()) + min, nil
}
