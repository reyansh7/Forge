package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/githubapi"
	"github.com/reyansh7/Forge/internal/store"
)

const maxWebhookBody = 1 << 20

// githubWebhook handles POST /webhooks/github/{id}.
//
// Public (no session): GitHub cannot present a Forge cookie. Trust is
// HMAC-SHA256 of the raw body with the per-binding webhook secret.
// Invalid signatures are 401. Duplicate X-GitHub-Delivery is 200 so
// GitHub stops retrying. A matching push reuses createQueuedDeployment
// + enqueueQueued — the same TypeDeploy path as the dashboard Deploy
// button. Wrong branch, deleted ref, or in-progress deploy is 200
// ignored (GitHub retries 4xx).
func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	if s.GitHubAccounts == nil || s.Deployments == nil || s.Jobs == nil {
		writeError(w, http.StatusInternalServerError, "webhooks are not configured")
		return
	}
	applicationID, err := store.ParseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook body")
		return
	}

	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	delivery := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	sig := strings.TrimSpace(r.Header.Get("X-Hub-Signature-256"))
	if delivery == "" {
		writeError(w, http.StatusBadRequest, "missing delivery id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	binding, err := s.GitHubAccounts.GetApplicationGitHub(ctx, applicationID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": "unknown_application"})
		return
	}
	if err != nil {
		s.logger().Error("load github binding failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to handle webhook")
		return
	}
	if !githubapi.VerifySignature(binding.WebhookSecret, sig, body) {
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}

	if event == "ping" {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "ping"})
		return
	}
	if event != "push" {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": "event"})
		return
	}

	push, err := githubapi.ParsePush(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid push payload")
		return
	}
	if !strings.EqualFold(push.Repo, binding.RepoFullName) {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": "repo"})
		return
	}
	if push.Deleted || push.Branch == "" || !strings.EqualFold(push.Branch, binding.Branch) {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": "branch"})
		return
	}

	fresh, err := s.GitHubAccounts.ClaimWebhookDelivery(ctx, delivery)
	if err != nil {
		s.logger().Error("claim webhook delivery failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to handle webhook")
		return
	}
	if !fresh {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": "duplicate"})
		return
	}

	commit := store.SanitizeCommitMeta(store.CommitMeta{
		SHA:     push.CommitSHA,
		Message: push.Message,
		Author:  push.Author,
	})
	d, prep := s.createQueuedDeployment(ctx, binding.ApplicationID, binding.Environment, binding.OwnerID, commit, true)
	if prep.ignored != "" {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": prep.ignored})
		return
	}
	if prep.message != "" {
		// Unknown failures stay 5xx so GitHub retries. Do not leak
		// internal strings beyond the generic writeError message.
		writeError(w, prep.status, prep.message)
		return
	}
	s.enqueueQueued(w, r, ctx, d)
}

func sessionActor(s *Server, r *http.Request) (Actor, bool) {
	if s == nil || s.Auth == nil {
		return Actor{}, false
	}
	raw := bearerOrCookie(r)
	if raw == "" {
		return Actor{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	u, err := s.Auth.LookupSession(ctx, store.HashSessionToken(raw))
	if err != nil {
		return Actor{}, false
	}
	return Actor{UserID: u.ID, Username: u.Username}, true
}
