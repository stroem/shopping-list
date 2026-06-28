# Suggest rate-limit design (issue #45)

## Problem
`GET /v1/suggest` runs trigram-similarity queries and is effectively public
(device-id only, no auth). The DB pool is tiny (`MaxConns: 4`, scale-to-zero
Neon). Without a limit, one client can exhaust the pool and run up DB cost,
violating the "cost ≈ 0" invariant. We need a per-client rate limit on suggest.

## Approach
Use `github.com/go-chi/httprate`. Apply the limiter as middleware on the `/v1`
route group only — `/healthz` (liveness) lives outside `/v1` and is never
limited. Over-limit requests return `429 Too Many Requests` with our standard
JSON error body (`{"error": "rate limit exceeded"}`) via
`httprate.WithLimitHandler`, not httprate's default plain-text body.

Ordering: the global middleware chain is cors → RequestID → Recoverer →
Timeout → DeviceIDMiddleware → routes. The limiter is added *inside* the `/v1`
group, so it runs after `DeviceIDMiddleware` and can read `web.DeviceID(ctx)`.

## Key function
Per-client key, device-id first with IP fallback:

```go
func suggestRateKey(r *http.Request) (string, error) {
    if deviceID := web.DeviceID(r.Context()); deviceID != "" {
        return "dev:" + deviceID, nil
    }
    return httprate.KeyByIP(r)
}
```

## In-process vs shared (decision) + serverless caveat
**Decision: in-process** (httprate's default local in-memory counter). A shared
store (Redis/DynamoDB) would add always-on cost and per-request latency,
breaking the cost ≈ 0 invariant for a public, low-stakes autocomplete.
**Serverless caveat:** under Lambda each warm instance keeps its own counter, so
the effective ceiling is `limit × concurrent instances`. This is a coarse abuse
guard that protects the small DB pool, not a precise global quota — acceptable
for this endpoint. Documented in `cmd/lambda/main.go` and `.env.example`.

## Config
`config.Config` gains `SuggestRateLimit int` (`SUGGEST_RATE_LIMIT`, default 60)
and `SuggestRateWindow time.Duration` (`SUGGEST_RATE_WINDOW`, default `1m`).
Invalid/empty values fall back to defaults (a typo can never disable limiting).
Threaded into `router.Deps`; non-positive values fall back to defaults so plain
`Deps{}` tests stay safe. `cmd/lambda` reads the envs directly (it skips
`config.Load`) via exported `config.SuggestRateLimit` / `SuggestRateWindow`.

## Affected files
- `internal/config/config.go` (+ test): new fields, parsers, defaults.
- `internal/router/ratelimit.go` (new): limiter + key func.
- `internal/router/router.go`: `Deps` fields + `/v1` group middleware.
- `internal/router/ratelimit_test.go` (new): burst→429, per-device, healthz exempt.
- `cmd/api/main.go`, `cmd/lambda/main.go`: wiring.
- `.env.example`: documented env vars.

## Test strategy
Build the router with a low limit and a long window (so a burst lands in one
window). Fire limit+1 requests with the same `X-Device-Id`: first `limit`
return 200, the next returns JSON 429. Assert per-device keying (two devices
each get their own budget) and that `/healthz` hammered past the limit never
429s. Fake `Suggester`/`Pinger` keep it DB-free; DB-backed tests still skip
without `DATABASE_URL`.
