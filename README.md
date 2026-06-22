# pwgen-api

A small, efficient Go REST API that builds passwords by fanning out one request
per character to a backend "router" service, then shuffling the result.

## Endpoint

```
GET /password/:length
```

- `:length` — a positive integer (max `4096`) for the password length.
- Issues `length` concurrent (bounded to 32 in flight) requests to the backend
  defined by `ROUTER_SERVICE`. Each backend response contributes one character.
- The assembled characters are shuffled (crypto-seeded Fisher-Yates) a random
  number of times between **2 and 10**.
- Returns JSON: `{"password": "...", "length": N}`.

Auxiliary endpoints:

- `GET /healthz` — liveness probe.
- `GET /metrics` — Prometheus scrape endpoint.

## Configuration

| Env var                        | Required | Default          | Description                                              |
| ------------------------------ | -------- | ---------------- | -------------------------------------------------------- |
| `ROUTER_SERVICE`               | yes      | —                | URL of the backend character source.                     |
| `LISTEN_ADDR`                  | no       | `:8080`          | Address the HTTP server binds to.                        |
| `LOG_LEVEL`                    | no       | `info`           | `debug` \| `info` \| `warn` \| `error`.                  |
| `OTEL_EXPORTER_OTLP_ENDPOINT`  | no       | `localhost:4317` | OTLP/gRPC agent endpoint for traces.                     |
| `OTEL_SERVICE_NAME` / resource | no       | `pwgen-api`      | Standard OTEL resource configuration is honoured.        |

## Observability

- **Tracing** — OpenTelemetry, exported over OTLP/gRPC to a collector/agent.
  Incoming W3C `traceparent`/`tracestate`/`baggage` headers are honoured
  (via `otelgin`) and propagated to every backend request (via `otelhttp`).
- **Metrics** — OpenTelemetry metrics exposed through the Prometheus exporter at
  `/metrics`, including `password_requests_total`,
  `password_request_failures_total`, and `password_generation_duration_seconds`.
- **Logging** — all logs are JSON (`log/slog`) and enriched with the active
  `trace_id` / `span_id`.

## Run locally

```sh
export ROUTER_SERVICE="http://localhost:9099"
# Disable trace export if you have no collector running:
export OTEL_TRACES_EXPORTER=none
go run .

curl http://localhost:8080/password/16
```
