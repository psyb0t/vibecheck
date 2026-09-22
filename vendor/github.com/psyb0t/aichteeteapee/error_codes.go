package aichteeteapee

import "net/http"

type ErrorCode = string

const (
	// Standard HTTP error codes.
	ErrorCodeBadRequest          ErrorCode = "BAD_REQUEST"
	ErrorCodeUnauthorized        ErrorCode = "UNAUTHORIZED"
	ErrorCodeForbidden           ErrorCode = "FORBIDDEN"
	ErrorCodeNotFound            ErrorCode = "NOT_FOUND"
	ErrorCodeMethodNotAllowed    ErrorCode = "METHOD_NOT_ALLOWED"
	ErrorCodeConflict            ErrorCode = "CONFLICT"
	ErrorCodeGone                ErrorCode = "GONE"
	ErrorCodeUnprocessableEntity ErrorCode = "UNPROCESSABLE_ENTITY"
	ErrorCodeTooManyRequests     ErrorCode = "TOO_MANY_REQUESTS"
	ErrorCodeInternalServerError ErrorCode = "INTERNAL_SERVER_ERROR"
	ErrorCodeNotImplemented      ErrorCode = "NOT_IMPLEMENTED"
	ErrorCodeBadGateway          ErrorCode = "BAD_GATEWAY"
	ErrorCodeServiceUnavailable  ErrorCode = "SERVICE_UNAVAILABLE"
	ErrorCodeGatewayTimeout      ErrorCode = "GATEWAY_TIMEOUT"

	// Semantic error codes.
	ErrorCodeValidationFailed ErrorCode = "VALIDATION_FAILED"
	ErrorCodeRateLimited      ErrorCode = "RATE_LIMITED"

	// Endpoint / routing errors.
	ErrorCodeEndpointNotFound ErrorCode = "ENDPOINT_NOT_FOUND"

	// File and path errors.
	ErrorCodeFileNotFound                 ErrorCode = "FILE_NOT_FOUND"
	ErrorCodeDirectoryListingNotSupported ErrorCode = "DIRECTORY_LISTING_" +
		"NOT_SUPPORTED"
	ErrorCodePathTraversalDenied ErrorCode = "PATH_TRAVERSAL_DENIED"

	// User-related errors.
	ErrorCodeMissingUserID ErrorCode = "MISSING_USER_ID"
	ErrorCodeInvalidUserID ErrorCode = "INVALID_USER_ID"

	// Content type errors.
	ErrorCodeMissingContentType     ErrorCode = "MISSING_CONTENT_TYPE"
	ErrorCodeUnsupportedContentType ErrorCode = "UNSUPPORTED_CONTENT_TYPE"

	// File upload errors.
	ErrorCodeInvalidMultipartForm ErrorCode = "INVALID_MULTIPART_FORM"
	ErrorCodeNoFileProvided       ErrorCode = "NO_FILE_PROVIDED"
	ErrorCodeFileSaveFailed       ErrorCode = "FILE_SAVE_FAILED"
)

//nolint:gochecknoglobals
var httpStatusToErrorCode = map[int]ErrorCode{
	http.StatusBadRequest:          ErrorCodeBadRequest,
	http.StatusUnauthorized:        ErrorCodeUnauthorized,
	http.StatusForbidden:           ErrorCodeForbidden,
	http.StatusNotFound:            ErrorCodeNotFound,
	http.StatusMethodNotAllowed:    ErrorCodeMethodNotAllowed,
	http.StatusConflict:            ErrorCodeConflict,
	http.StatusGone:                ErrorCodeGone,
	http.StatusUnprocessableEntity: ErrorCodeUnprocessableEntity,
	http.StatusTooManyRequests:     ErrorCodeTooManyRequests,
	http.StatusInternalServerError: ErrorCodeInternalServerError,
	http.StatusNotImplemented:      ErrorCodeNotImplemented,
	http.StatusBadGateway:          ErrorCodeBadGateway,
	http.StatusServiceUnavailable:  ErrorCodeServiceUnavailable,
	http.StatusGatewayTimeout:      ErrorCodeGatewayTimeout,
}

// Returns ErrorCodeInternalServerError for unmapped status codes.
func ErrorCodeFromHTTPStatus(status int) ErrorCode {
	code, ok := httpStatusToErrorCode[status]
	if !ok {
		return ErrorCodeInternalServerError
	}

	return code
}
