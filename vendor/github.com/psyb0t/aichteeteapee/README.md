# aichteeteapee

[![Go Reference](https://pkg.go.dev/badge/github.com/psyb0t/aichteeteapee.svg)](https://pkg.go.dev/github.com/psyb0t/aichteeteapee)
[![CI](https://github.com/psyb0t/aichteeteapee/actions/workflows/pipeline.yml/badge.svg?branch=main)](https://github.com/psyb0t/aichteeteapee/actions/workflows/pipeline.yml)
[![coverage](https://raw.githubusercontent.com/psyb0t/aichteeteapee/badges/coverage.svg)](https://github.com/psyb0t/aichteeteapee/actions/workflows/pipeline.yml)
[![version](https://raw.githubusercontent.com/psyb0t/aichteeteapee/badges/version.svg)](https://github.com/psyb0t/aichteeteapee/tags)
[![license](https://raw.githubusercontent.com/psyb0t/aichteeteapee/badges/license.svg)](LICENSE)
[![imported by](https://raw.githubusercontent.com/psyb0t/aichteeteapee/badges/importers.svg)](https://github.com/psyb0t/aichteeteapee/blob/badges/importers.md)

Pronounced "HTTP". The name is the whole joke. Moving on.

A Go HTTP library that does everything you need and nothing you don't. Spin up a production-ready server with middleware, WebSocket hubs, file uploads, static serving, request proxying with caching, and OpenAPI validation — all with secure defaults and zero boilerplate.

```go
srv, _ := serbewr.New()

router := &serbewr.Router{
    GlobalMiddlewares: []middleware.Middleware{
        middleware.RequestID(),
        middleware.Logger(),
        middleware.Recovery(),
        middleware.SecurityHeaders(),
        middleware.CORS(),
    },
    Groups: []serbewr.GroupConfig{{
        Path: "/",
        Routes: []serbewr.RouteConfig{{
            Method:  http.MethodGet,
            Path:    "/hello",
            Handler: func(w http.ResponseWriter, _ *http.Request) {
                aichteeteapee.WriteJSON(w, http.StatusOK, map[string]string{"msg": "hi"})
            },
        }},
    }},
}

srv.Start(ctx, router)
```

## Security

Secure by default. CORS blocks unknown origins, WebSocket validates `Origin` against `Host`, file uploads sanitize filenames (no path traversal), uploaded files get `0600` permissions and won't overwrite existing files, request IDs are validated, proxy responses are size-limited, sensitive headers are filtered from echo responses.

Need to skip all that during local dev? One call:

```go
aichteeteapee.FuckSecurity()
```

CORS allows all origins, WebSocket accepts any origin. Call `aichteeteapee.UnfuckSecurity()` to go back.

Per-component overrides without global dev mode:

```go
middleware.CORS(middleware.WithAllowAllOrigins())

wshub.UpgradeHandler(hub,
    wshub.WithUpgradeHandlerCheckOrigin(
        aichteeteapee.GetPermissiveWebSocketCheckOrigin,
    ),
)
```

## What's in the box

### Root package — constants and utilities

Everything you need to stop hardcoding strings in your HTTP code.

- **Content types** — `ContentTypeJSON`, `ContentTypeYAML`, `ContentTypeHTML`, `ContentTypeMultipartFormData`, etc.
- **Header names** — every header you'll ever need as a constant. Authentication, content negotiation, CORS, cache, security, hop-by-hop, rate limiting, WebSocket, you name it. Plus `AuthSchemeBearer` and `AuthSchemeBasic` for scheme prefixes.
- **Error handling** — `ErrorCode` constants for every HTTP status, `ErrorCodeFromHTTPStatus()` mapper, sentinel errors (`ErrBadRequest`, `ErrNotFound`, ...), pre-built `ErrorResponse` structs ready to serialize.
- **Request utilities** — `GetClientIP(r)`, `GetRequestID(r)`, content type checkers (`IsRequestContentTypeJSON`, etc.).
- **Response utilities** — `WriteJSON(w, statusCode, data)`.
- **Network constants** — `SchemeHTTP`, `SchemeHTTPS`, `NetworkTypeTCP`, `NetworkTypeUnix`, etc.

### [`serbewr/`](docs/server.md) — HTTP server

Pronounced "server". D'oooh you kno. Built on `net/http` with Go 1.22+ `ServeMux` routing, grouped routes with per-group middleware, built-in handlers (health, echo, file upload), static file serving with directory indexing, TLS, and config from env vars or code. [Full docs](docs/server.md).

### [`serbewr/middleware/`](docs/middleware.md) — middleware stack

RequestID, Logger, Recovery, BasicAuth, BearerAuth, CORS, SecurityHeaders, Timeout, EnforceRequestContentType. All use context-propagated structured logging — set it up once, every log line gets the full request context for free. [Full docs](docs/middleware.md).

### [`serbewr/prawxxey/`](docs/proxy.md) — request forwarding

Pronounced "proxy", because of course it is. Forward requests upstream with optional response caching, hop-by-hop header stripping, response size limits, and deterministic request fingerprinting. [Full docs](docs/proxy.md).

### [`serbewr/dabluvee-es/`](docs/websocket.md) — WebSocket event system

Pronounced "WS" — because why stop at one wordplay. Three-tier architecture (Hub -> Client -> Connection) with typed events, handler registration, broadcast, and a Unix socket bridge for when you need to plug in external tools. [Full docs](docs/websocket.md).

### [`echo/`](docs/echo.md) — Echo framework integration

For when you want [labstack/echo](https://github.com/labstack/echo) instead of `net/http`. Wrapper with auto-served OpenAPI specs, Swagger UI, Bearer auth middleware, and OpenAPI request validation via oapi-codegen. [Full docs](docs/echo.md).

### [`klhayyent/`](docs/client.md), HTTP client

Pronounced "client". Apparently that one needed saying too. Handles outbound
HTTP without making every service rebuild the same wrapper: JSON, raw, text,
and form bodies, bounded responses, per-request headers and query values,
request ID propagation, same-origin redirects, and a real cookie jar. Non-2xx
responses use the same `ErrorResponse` envelope as the server packages. [Full
docs](docs/client.md).

## Logging

All middleware and proxy code uses [ctxscope](https://github.com/psyb0t/ctxscope) for context-propagated structured logging.

The middleware chain builds up the request's scope progressively:
1. **RequestID** puts `requestId` on the scope
2. **Logger** adds `method`, `path`, `ip`
3. Downstream code calls `ctxscope.GetLogger(ctx)` and gets all fields automatically

The context carries attributes, not a logger. `ctxscope.GetLogger` applies them to `slog.Default()` at the moment you ask, so configure your handler once at startup with `slog.SetDefault` and every line picks it up. Because they are data rather than a logger, the same attributes also cross a process boundary via `ctxscope.ToJSON` / `ctxscope.FromJSON`.

Field names come from the constants in `log_fields.go` — `FieldRequestID`, `FieldMethod`, `FieldPath`, `FieldIP` and friends.

No explicit logger passing. Every log line from every middleware and handler automatically includes the full request context.

## Development

Every target runs inside a pinned dev image (Go 1.26.6 + the linter and
scanners), so a local run and CI use the exact same toolchain.

```bash
make dev-image      # build the sandboxed dev image
make dep            # go mod tidy + vendor
make format         # format Go and shell source
make lint           # golangci-lint (strict)
make lint-fix       # lint + auto-fix
make test           # go test -race ./...
make test-coverage  # coverage with 90% minimum
make sec            # gitleaks + govulncheck + semgrep; gates on findings
```

## License

MIT. See [LICENSE](LICENSE).
