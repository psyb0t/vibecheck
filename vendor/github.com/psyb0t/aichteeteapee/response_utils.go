package aichteeteapee

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

func WriteJSON(
	w http.ResponseWriter,
	statusCode int,
	data any,
) {
	w.Header().Set(
		HeaderNameContentType,
		ContentTypeJSON,
	)
	w.WriteHeader(statusCode)

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ") // Pretty print for better readability

	if err := encoder.Encode(data); err != nil {
		slog.Error(
			"failed to encode JSON response",
			"err", err,
		)
	}
}
