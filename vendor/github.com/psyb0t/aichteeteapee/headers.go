package aichteeteapee

const (
	// Authentication.
	HeaderNameAuthorization   = "Authorization"
	HeaderNameXAPIKey         = "X-Api-Key" //nolint: gosec
	HeaderNameWWWAuthenticate = "WWW-Authenticate"

	// Authentication schemes.
	AuthSchemeBearer = "Bearer "
	AuthSchemeBasic  = "Basic "

	// Session/cookie.
	HeaderNameCookie    = "Cookie"
	HeaderNameSetCookie = "Set-Cookie"

	// Content negotiation.
	HeaderNameContentType        = "Content-Type"
	HeaderNameContentLength      = "Content-Length"
	HeaderNameContentDisposition = "Content-Disposition"
	HeaderNameContentEncoding    = "Content-Encoding"
	HeaderNameContentLanguage    = "Content-Language"
	HeaderNameContentLocation    = "Content-Location"
	HeaderNameContentRange       = "Content-Range"
	HeaderNameAccept             = "Accept"
	HeaderNameAcceptCharset      = "Accept-Charset"
	HeaderNameAcceptEncoding     = "Accept-Encoding"
	HeaderNameAcceptLanguage     = "Accept-Language"
	HeaderNameAcceptRanges       = "Accept-Ranges"

	// Request tracking.
	HeaderNameXRequestID     = "X-Request-ID"
	HeaderNameXCorrelationID = "X-Correlation-ID"

	// Client info.
	HeaderNameUserAgent       = "User-Agent"
	HeaderNameXForwardedFor   = "X-Forwarded-For"
	HeaderNameXForwardedProto = "X-Forwarded-Proto"
	HeaderNameXForwardedHost  = "X-Forwarded-Host"
	HeaderNameXRealIP         = "X-Real-IP"
	HeaderNameXClientID       = "X-Client-ID"
	HeaderNameHost            = "Host"
	HeaderNameReferer         = "Referer"

	// CORS.
	HeaderNameOrigin                        = "Origin"
	HeaderNameAccessControlAllowOrigin      = "Access-Control-Allow-Origin"
	HeaderNameAccessControlAllowMethods     = "Access-Control-Allow-Methods"
	HeaderNameAccessControlAllowHeaders     = "Access-Control-Allow-Headers"
	HeaderNameAccessControlExposeHeaders    = "Access-Control-Expose-Headers"
	HeaderNameAccessControlAllowCredentials = "Access-Control-Allow-Credentials"
	HeaderNameAccessControlMaxAge           = "Access-Control-Max-Age"
	HeaderNameAccessControlRequestMethod    = "Access-Control-Request-Method"
	HeaderNameAccessControlRequestHeaders   = "Access-Control-Request-Headers"
	HeaderNameVary                          = "Vary"

	// Cache control.
	HeaderNameCacheControl = "Cache-Control"
	HeaderNamePragma       = "Pragma"
	HeaderNameExpires      = "Expires"
	HeaderNameETag         = "ETag"
	HeaderNameIfNoneMatch  = "If-None-Match"
	HeaderNameIfMatch      = "If-Match"
	HeaderNameIfModSince   = "If-Modified-Since"
	HeaderNameIfUnmodSince = "If-Unmodified-Since"
	HeaderNameLastModified = "Last-Modified"
	HeaderNameAge          = "Age"
	HeaderNameXCacheStatus = "X-Cache-Status"

	// Hop-by-hop (RFC 2616 section 13.5.1) — must not be forwarded by proxies.
	HeaderNameConnection         = "Connection"
	HeaderNameKeepAlive          = "Keep-Alive"
	HeaderNameProxyAuthenticate  = "Proxy-Authenticate"
	HeaderNameProxyAuthorization = "Proxy-Authorization"
	HeaderNameTE                 = "Te"
	HeaderNameTrailer            = "Trailer"
	// HeaderNameTrailers is kept for compatibility with older callers.
	HeaderNameTrailers         = "Trailers"
	HeaderNameTransferEncoding = "Transfer-Encoding"
	HeaderNameUpgrade          = "Upgrade"

	// Security.
	HeaderNameStrictTransportSecurity = "Strict-Transport-Security"
	HeaderNameXContentTypeOptions     = "X-Content-Type-Options"
	HeaderNameXFrameOptions           = "X-Frame-Options"
	HeaderNameXXSSProtection          = "X-XSS-Protection"
	HeaderNameReferrerPolicy          = "Referrer-Policy"
	HeaderNameContentSecurityPolicy   = "Content-Security-Policy"
	HeaderNamePermissionsPolicy       = "Permissions-Policy"
	HeaderNameCrossOriginOpenerPolicy = "Cross-Origin-Opener-Policy"
	HeaderNameCrossOriginEmbedPolicy  = "Cross-Origin-Embedder-Policy"
	HeaderNameCrossOriginResourcePol  = "Cross-Origin-Resource-Policy"
	HeaderNameXDNSPrefetchControl     = "X-DNS-Prefetch-Control"
	HeaderNameXDownloadOptions        = "X-Download-Options"
	HeaderNameXPermittedCrossDomain   = "X-Permitted-Cross-Domain-Policies"

	// Rate limiting.
	HeaderNameRetryAfter       = "Retry-After"
	HeaderNameXRateLimitLimit  = "X-RateLimit-Limit"
	HeaderNameXRateLimitRemain = "X-RateLimit-Remaining"
	HeaderNameXRateLimitReset  = "X-RateLimit-Reset"

	// Response metadata.
	HeaderNameLocation = "Location"
	HeaderNameAllow    = "Allow"
	HeaderNameServer   = "Server"
	HeaderNameDate     = "Date"

	// WebSocket.
	HeaderNameSecWebSocketKey       = "Sec-WebSocket-Key"
	HeaderNameSecWebSocketVersion   = "Sec-WebSocket-Version"
	HeaderNameSecWebSocketProtocol  = "Sec-WebSocket-Protocol"
	HeaderNameSecWebSocketExtension = "Sec-WebSocket-Extensions"
	HeaderNameSecWebSocketAccept    = "Sec-WebSocket-Accept"

	// Misc.
	HeaderNameXPoweredBy = "X-Powered-By"
	HeaderNameDNT        = "DNT"
	HeaderNameExpect     = "Expect"
	HeaderNameFrom       = "From"
	HeaderNameRange      = "Range"
	HeaderNameWarning    = "Warning"
)

const (
	CacheStatusHit  = "HIT"
	CacheStatusMiss = "MISS"
)
