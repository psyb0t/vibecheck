//nolint:gochecknoglobals // Error responses are intentionally global for reuse
package aichteeteapee

type ErrorResponse struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Details any    `json:"details,omitempty"`
}

var (
	// File and path errors.
	ErrorResponseFileNotFound = ErrorResponse{
		Code:    ErrorCodeFileNotFound,
		Message: "File not found",
	}

	ErrorResponseDirectoryListingNotSupported = ErrorResponse{
		Code:    ErrorCodeDirectoryListingNotSupported,
		Message: "Directory listing is not supported",
	}

	ErrorResponsePathTraversalDenied = ErrorResponse{
		Code:    ErrorCodePathTraversalDenied,
		Message: "Path traversal denied",
	}

	// Standard HTTP errors.
	ErrorResponseNotFound = ErrorResponse{
		Code:    ErrorCodeNotFound,
		Message: "Not found",
	}

	ErrorResponseEndpointNotFound = ErrorResponse{
		Code:    ErrorCodeEndpointNotFound,
		Message: "Endpoint not found",
	}

	ErrorResponseMethodNotAllowed = ErrorResponse{
		Code:    ErrorCodeMethodNotAllowed,
		Message: "Method not allowed",
	}

	ErrorResponseConflict = ErrorResponse{
		Code:    ErrorCodeConflict,
		Message: "Conflict",
	}

	ErrorResponseGone = ErrorResponse{
		Code:    ErrorCodeGone,
		Message: "Gone",
	}

	ErrorResponseUnprocessableEntity = ErrorResponse{
		Code:    ErrorCodeUnprocessableEntity,
		Message: "Unprocessable entity",
	}

	ErrorResponseTooManyRequests = ErrorResponse{
		Code:    ErrorCodeTooManyRequests,
		Message: "Too many requests",
	}

	ErrorResponseNotImplemented = ErrorResponse{
		Code:    ErrorCodeNotImplemented,
		Message: "Not implemented",
	}

	ErrorResponseBadGateway = ErrorResponse{
		Code:    ErrorCodeBadGateway,
		Message: "Bad gateway",
	}

	ErrorResponseServiceUnavailable = ErrorResponse{
		Code:    ErrorCodeServiceUnavailable,
		Message: "Service unavailable",
	}

	ErrorResponseGatewayTimeout = ErrorResponse{
		Code:    ErrorCodeGatewayTimeout,
		Message: "Gateway timeout",
	}

	// User-related errors.
	ErrorResponseMissingUserID = ErrorResponse{
		Code:    ErrorCodeMissingUserID,
		Message: "User ID is required",
	}

	ErrorResponseInvalidUserID = ErrorResponse{
		Code:    ErrorCodeInvalidUserID,
		Message: "Invalid user ID format",
	}

	// Generic errors.
	ErrorResponseValidationFailed = ErrorResponse{
		Code:    ErrorCodeValidationFailed,
		Message: "Validation failed",
	}

	ErrorResponseBadRequest = ErrorResponse{
		Code:    ErrorCodeBadRequest,
		Message: "Bad request",
	}

	ErrorResponseUnauthorized = ErrorResponse{
		Code:    ErrorCodeUnauthorized,
		Message: "Unauthorized",
	}

	ErrorResponseForbidden = ErrorResponse{
		Code:    ErrorCodeForbidden,
		Message: "Access forbidden",
	}

	ErrorResponseInternalServerError = ErrorResponse{
		Code:    ErrorCodeInternalServerError,
		Message: "Internal server error",
	}

	// Content type errors.
	ErrorResponseMissingContentType = ErrorResponse{
		Code:    ErrorCodeMissingContentType,
		Message: "Content-Type header is required",
	}

	ErrorResponseUnsupportedContentType = ErrorResponse{
		Code:    ErrorCodeUnsupportedContentType,
		Message: "Unsupported content type",
	}

	// File upload errors.
	ErrorResponseInvalidMultipartForm = ErrorResponse{
		Code:    ErrorCodeInvalidMultipartForm,
		Message: "Invalid multipart form",
	}

	ErrorResponseNoFileProvided = ErrorResponse{
		Code:    ErrorCodeNoFileProvided,
		Message: "No file provided",
	}

	ErrorResponseFileSaveFailed = ErrorResponse{
		Code:    ErrorCodeFileSaveFailed,
		Message: "Failed to save file",
	}
)
