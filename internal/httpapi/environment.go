package httpapi

import (
	"net/http"

	"github.com/reyansh7/Forge/internal/store"
)

type environmentBody struct {
	Environment string `json:"environment"`
}

func parseRequestEnvironment(r *http.Request, bodyEnv string) (string, error) {
	if q := r.URL.Query().Get("environment"); q != "" {
		return store.ParseEnvironment(q)
	}
	return store.ParseEnvironment(bodyEnv)
}

func readEnvironment(r *http.Request, w http.ResponseWriter, optionalBody bool) (string, bool) {
	var req environmentBody
	if optionalBody {
		if !decodeOptionalJSON(r, w, &req) {
			return "", false
		}
	} else if !decodeJSON(r, w, &req) {
		return "", false
	}
	env, err := parseRequestEnvironment(r, req.Environment)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return env, true
}

func envOf(d store.Deployment) string {
	e, err := store.ParseEnvironment(d.Environment)
	if err != nil {
		return store.EnvProduction
	}
	return e
}
