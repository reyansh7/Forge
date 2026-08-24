package httpapi

import (
	"encoding/json"
	"net/http"
)

// maxJSONBody is a request-size limit, not a Project-size limit.
// Without MaxBytesReader, a client could stream gigabytes into RAM
// (a cheap denial-of-service). 64 KiB is far larger than any valid
// create-project JSON in this increment.
const maxJSONBody = 64 << 10

// errorBody is the JSON shape for 4xx/5xx. Callers should not receive
// raw SQL or driver strings — those stay in server logs.
type errorBody struct {
	Error string `json:"error"`
}

// writeJSON sends status then a JSON body. Content-Type must be set
// before WriteHeader: after the status is flushed, headers are frozen.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// decodeJSON reads r.Body into dst. false means the handler already
// wrote 400 and must return without touching the store.
func decodeJSON(r *http.Request, w http.ResponseWriter, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}
