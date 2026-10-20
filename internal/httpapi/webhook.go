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
// Invalid signatures are 401. A matching push reuses createQueuedDeployment
// and the same TypeDeploy enqueue as the dashboard Deploy button.
// Wrong branch, deleted ref, or an intentional skip is 200 so GitHub
// does not retry. A delivery id is marked queued only after that
// decision. Redis or database failure marks it failed and returns 5xx,
// so GitHub's retry re-queues the same deployment instead of being
// told the push already ran.
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

	lease, err := s.GitHubAccounts.BeginWebhookDelivery(ctx, delivery)
	if err != nil {
		s.logger().Error("begin webhook delivery failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to handle webhook")
		return
	}
	if !lease.Proceed {
		if lease.Busy {
			writeError(w, http.StatusServiceUnavailable, "webhook delivery is still in progress")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ignored": "duplicate"})
		return
	}

	commit := store.SanitizeCommitMeta(store.CommitMeta{
		SHA:     push.CommitSHA,
		Message: push.Message,
		Author:  push.Author,
	})
	if handled, settle, depID := s.webhookProjectDeploy(w, r, ctx, binding, commit); handled {
		s.finishDelivery(ctx, delivery, lease, settle, depID)
		return
	}
	if s.recoverWebhookDeployment(w, r, ctx, delivery, lease) {
		return
	}
	d, prep := s.createQueuedDeployment(ctx, binding.ApplicationID, binding.Environment, binding.OwnerID, commit, true)
	if prep.ignored != "" {
		s.finishDelivery(ctx, delivery, lease, store.DeliveryQueued, d.ID)
		writeJSON(w, http.StatusOK, map[string]string{"ignored": prep.ignored})
		return
	}
	if prep.message != "" {
		// Unknown failures stay 5xx so GitHub retries. The delivery
		// stays failed so that retry is not classified as a duplicate.
		s.finishDelivery(ctx, delivery, lease, store.DeliveryFailed, d.ID)
		writeError(w, prep.status, prep.message)
		return
	}
	out := s.enqueueDeploymentJob(r, ctx, d)
	if out.status != http.StatusAccepted {
		s.finishDelivery(ctx, delivery, lease, store.DeliveryFailed, d.ID)
		writeError(w, out.status, out.message)
		return
	}
	s.finishDelivery(ctx, delivery, lease, store.DeliveryQueued, out.deployment.ID)
	w.Header().Set("Location", "/deployments/"+out.deployment.ID)
	writeJSON(w, http.StatusAccepted, createDeploymentResponse{
		deploymentResponse: deploymentResponseFrom(out.deployment),
		JobID:              out.jobID,
		Queued:             "queued",
	})
}

// finishDelivery records the attempt outcome. A lost race (stale
// attempt number) is logged and does not change the HTTP result:
// the newer attempt owns the row.
func (s *Server) finishDelivery(ctx context.Context, deliveryID string, lease store.WebhookLease, state, deploymentID string) {
	if state == "" {
		state = store.DeliveryQueued
	}
	if err := s.GitHubAccounts.FinishWebhookDelivery(ctx, deliveryID, lease.Attempt, state, deploymentID); err != nil {
		s.logger().Error("finish webhook delivery failed", "err", err, "state", state)
	}
}

// recoverWebhookDeployment re-queues the deployment this delivery
// already created when Redis rejected the first push. It does not
// insert a second row. A deployment that has already left queued
// is left alone and the delivery is closed.
func (s *Server) recoverWebhookDeployment(w http.ResponseWriter, r *http.Request, ctx context.Context, deliveryID string, lease store.WebhookLease) bool {
	if !lease.Recover || lease.DeploymentID == "" || s.Deployments == nil {
		return false
	}
	d, err := s.Deployments.GetDeployment(ctx, lease.DeploymentID)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		s.logger().Error("load webhook deployment failed", "err", err)
		s.finishDelivery(ctx, deliveryID, lease, store.DeliveryFailed, lease.DeploymentID)
		writeError(w, http.StatusInternalServerError, "failed to handle webhook")
		return true
	}
	if d.Status == store.StatusQueued {
		out := s.enqueueDeploymentJob(r, ctx, d)
		if out.status != http.StatusAccepted {
			s.finishDelivery(ctx, deliveryID, lease, store.DeliveryFailed, d.ID)
			writeError(w, out.status, out.message)
			return true
		}
		s.finishDelivery(ctx, deliveryID, lease, store.DeliveryQueued, out.deployment.ID)
		w.Header().Set("Location", "/deployments/"+out.deployment.ID)
		writeJSON(w, http.StatusAccepted, createDeploymentResponse{
			deploymentResponse: deploymentResponseFrom(out.deployment),
			JobID:              out.jobID,
			Queued:             "queued",
		})
		return true
	}
	s.finishDelivery(ctx, deliveryID, lease, store.DeliveryQueued, d.ID)
	writeJSON(w, http.StatusOK, map[string]string{"ignored": "already_recorded"})
	return true
}

// webhookProjectDeploy starts one project release when the repository
// has more than one deployable service. A single-service repository
// returns false and the caller keeps the existing one-application path.
// The settle state is queued when this delivery must not be retried,
// and failed when GitHub should send the same delivery again.
func (s *Server) webhookProjectDeploy(w http.ResponseWriter, r *http.Request, ctx context.Context, binding store.ApplicationGitHub, commit store.CommitMeta) (bool, string, string) {
	if s.Apps == nil || s.Projects == nil || s.ProjectDeploys == nil || s.Deployments == nil {
		return false, "", ""
	}
	app, err := s.Apps.GetApplication(ctx, binding.ApplicationID)
	if err != nil {
		return false, "", ""
	}
	project, err := s.Projects.GetProject(ctx, app.ProjectID)
	if err != nil {
		return false, "", ""
	}
	found, err := s.discoverProjectServices(ctx, binding.OwnerID, project)
	if err != nil {
		s.logger().Error("webhook project discover failed", "err", err)
		writeError(w, http.StatusBadGateway, "could not read the repository from GitHub")
		return true, store.DeliveryFailed, ""
	}
	selected := selectServices(found, nil)
	if len(selected) < 2 {
		return false, "", ""
	}
	if len(selected) > s.nodeInflightQuota() {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": "node_cap", "error": "this node will not run that many deployments at once"})
		return true, store.DeliveryQueued, ""
	}
	inFlight, err := s.Deployments.CountInProgressByOwner(ctx, binding.OwnerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create deployment")
		return true, store.DeliveryFailed, ""
	}
	if inFlight > 0 {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": "in_progress"})
		return true, store.DeliveryQueued, ""
	}
	actor := Actor{UserID: binding.OwnerID}
	view, status, msg := s.enqueueProjectServices(ctx, r, project, actor, selected, commit)
	depID := ""
	if len(view.Services) > 0 {
		depID = view.Services[0].DeploymentID
	}
	if status == http.StatusBadRequest {
		writeJSON(w, http.StatusOK, map[string]string{"ignored": "invalid_plan", "error": msg})
		return true, store.DeliveryQueued, depID
	}
	if status != http.StatusAccepted {
		writeError(w, status, msg)
		return true, store.DeliveryFailed, depID
	}
	writeJSON(w, http.StatusAccepted, view)
	return true, store.DeliveryQueued, depID
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
