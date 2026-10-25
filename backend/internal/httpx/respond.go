// Package httpx holds tiny HTTP response helpers with no dependency on any
// domain package, so every domain's handlers (and the router itself) can
// depend on it without creating import cycles.
package httpx

import (
	"encoding/json"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

// DecodeJSON reads and decodes a JSON request body, rejecting unknown
// fields so clients get an explicit error instead of silently-ignored
// typos, and rejecting a second JSON value in the body (defense against
// request smuggling via trailing garbage).
func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
