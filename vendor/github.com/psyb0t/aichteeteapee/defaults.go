package aichteeteapee

import (
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// Common API path defaults.
	DefaultAPIRootPath       = "/api"
	StandardAPIOASPath       = "/openapi.yaml"
	StandardAPISwaggerUIPath = "/docs/*"

	// Echo server defaults.
	DefaultEchoListenAddress = "0.0.0.0:8080"

	// Server defaults.
	DefaultHTTPServerListenAddress     = "127.0.0.1:8080"
	DefaultHTTPServerReadTimeout       = 15 * time.Second
	DefaultHTTPServerReadHeaderTimeout = 10 * time.Second
	DefaultHTTPServerWriteTimeout      = 30 * time.Second
	DefaultHTTPServerIdleTimeout       = 60 * time.Second
	DefaultHTTPServerMaxHeaderBytes    = 1 << 20 // 1MB
	DefaultHTTPServerShutdownTimeout   = 10 * time.Second
	DefaultHTTPServerServiceName       = "http-server"

	// TLS Server defaults.
	DefaultHTTPServerTLSEnabled       = false
	DefaultHTTPServerTLSListenAddress = "127.0.0.1:8443"
	DefaultHTTPServerTLSCertFile      = ""
	DefaultHTTPServerTLSKeyFile       = ""

	// Request defaults.
	DefaultHTTPRequestTimeout = 30 * time.Second
	DefaultHTTPClientTimeout  = 30 * time.Second

	// CORS defaults.
	DefaultCORSAllowOriginAll = "*"
	DefaultCORSMaxAge         = 86400 // 24 hours in seconds

	// Security header default values.
	DefaultSecurityXContentTypeOptionsNoSniff = "nosniff"
	DefaultSecurityXFrameOptionsDeny          = "DENY"
	DefaultSecurityXXSSProtectionBlock        = "1; mode=block"
	DefaultSecurityStrictTransportSecurity    = "max-age=31536000; " +
		"includeSubDomains"
	DefaultSecurityReferrerPolicyStrictOrigin = "strict-origin" +
		"-when-cross-origin"

	// Authentication default values.
	DefaultBasicRealmName      = "restricted"
	DefaultUnauthorizedMessage = "Unauthorized"

	// File upload defaults.
	DefaultFileUploadMaxMemory = int64(32 << 20)  // 32MB
	DefaultFileUploadMaxSize   = int64(100 << 20) // 100MB

	// WebSocket Client Configuration Defaults.
	DefaultWebSocketClientSendBufferSize  = 256
	DefaultWebSocketClientReadBufferSize  = 1024
	DefaultWebSocketClientWriteBufferSize = 1024
	DefaultWebSocketClientReadLimit       = 1024 * 1024 // 1MB
	DefaultWebSocketClientReadTimeout     = 60 * time.Second
	DefaultWebSocketClientWriteTimeout    = 10 * time.Second
	DefaultWebSocketClientPingInterval    = 54 * time.Second
	DefaultWebSocketClientPongTimeout     = 60 * time.Second

	// WebSocket Handler Configuration Defaults.
	DefaultWebSocketHandlerReadBufferSize    = 1024
	DefaultWebSocketHandlerWriteBufferSize   = 1024
	DefaultWebSocketHandlerHandshakeTimeout  = 45 * time.Second
	DefaultWebSocketHandlerEnableCompression = false
)

func GetDefaultCORSAllowMethods() string {
	return strings.Join([]string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodOptions,
	}, ", ")
}

func GetDefaultCORSAllowHeaders() string {
	return strings.Join([]string{
		HeaderNameAuthorization,
		HeaderNameContentType,
		HeaderNameXRequestID,
	}, ", ")
}

//nolint:gochecknoglobals
var devMode atomic.Bool

// FuckSecurity enables permissive defaults for quick local development.
// CORS allows all origins, WebSocket accepts any origin, etc.
// Call UnfuckSecurity() to restore secure defaults.
func FuckSecurity() {
	devMode.Store(true)
}

// UnfuckSecurity restores secure defaults after FuckSecurity.
func UnfuckSecurity() {
	devMode.Store(false)
}

// IsDevMode returns true if FuckSecurity was called.
func IsDevMode() bool {
	return devMode.Load()
}

// GetDefaultCORSAllowAllOrigins returns whether CORS should allow all origins.
// Secure default: false. Dev mode: true.
func GetDefaultCORSAllowAllOrigins() bool {
	return devMode.Load()
}

// GetDefaultWebSocketCheckOrigin returns the default origin checker for
// WebSocket connections. Secure default: validates Origin matches request Host.
// Dev mode: allows all origins.
func GetDefaultWebSocketCheckOrigin(r *http.Request) bool {
	if devMode.Load() {
		return true
	}

	origin := r.Header.Get(HeaderNameOrigin)
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	return strings.EqualFold(u.Host, r.Host)
}

// GetPermissiveWebSocketCheckOrigin always allows all origins.
// Use with WithUpgradeHandlerCheckOrigin when you need to bypass origin
// validation for a specific handler without enabling global dev mode.
func GetPermissiveWebSocketCheckOrigin(_ *http.Request) bool {
	return true
}
