package aichteeteapee

import (
	"net"
	"net/http"
	"strings"
)

func GetRequestID(r *http.Request) string {
	if reqID, ok := r.Context().Value(ContextKeyRequestID).(string); ok {
		return reqID
	}

	return ""
}

func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get(HeaderNameXForwardedFor); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}

	if xri := r.Header.Get(HeaderNameXRealIP); xri != "" {
		return strings.TrimSpace(xri)
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return ip
}

// Handles charset parameters and is case-insensitive.
func IsRequestContentType(r *http.Request, expectedContentType string) bool {
	contentType := r.Header.Get(HeaderNameContentType)
	if contentType == "" {
		return false
	}

	mediaType := strings.Split(contentType, ";")[0]
	mediaType = strings.TrimSpace(mediaType)

	return strings.EqualFold(mediaType, expectedContentType)
}

func IsRequestContentTypeJSON(r *http.Request) bool {
	return IsRequestContentType(r, ContentTypeJSON)
}

func IsRequestContentTypeXML(r *http.Request) bool {
	return IsRequestContentType(r, ContentTypeXML)
}

func IsRequestContentTypeApplicationFormURLEncoded(r *http.Request) bool {
	return IsRequestContentType(r, ContentTypeApplicationFormURLEncoded)
}

func IsRequestContentTypeMultipartFormData(r *http.Request) bool {
	return IsRequestContentType(r, ContentTypeMultipartFormData)
}
