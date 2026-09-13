package httpapi

// Node HTTP routes (GET/POST /nodes) are operator session routes.
// Placement is not computed here: create/list persist rows; enqueue
// calls schedule.Scheduler via Placer. Join tokens are returned once
// and never logged.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/store"
)

// Placer chooses a ready node. Implemented by *schedule.Scheduler.
// Handlers must not count heartbeats themselves.
type Placer interface {
	Place(ctx context.Context) (store.Node, error)
}

// JobSink is the HTTP → per-node Redis LIST boundary.
type JobSink interface {
	EnqueueOn(ctx context.Context, nodeID string, job queue.Job) error
}

// NodeRegistry is the HTTP → nodes table boundary.
//
// Create returns the raw join token once. List never includes token_hash.
// SetDeploymentNode is how a queued row becomes that node's problem.
type NodeRegistry interface {
	CreateNode(ctx context.Context, name, advertiseHost string) (store.Node, string, error)
	ListNodes(ctx context.Context) ([]store.Node, error)
	SetDeploymentNode(ctx context.Context, deploymentID, nodeID string) error
}

type createNodeRequest struct {
	Name          string `json:"name"`
	AdvertiseHost string `json:"advertise_host"`
}

type nodeResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	AdvertiseHost string    `json:"advertise_host,omitempty"`
	JoinToken     string    `json:"join_token,omitempty"`
	InProgress    int       `json:"in_progress"`
	LastSeenAt    string    `json:"last_seen_at,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func nodeResponseFrom(n store.Node, token string) nodeResponse {
	out := nodeResponse{
		ID:            n.ID,
		Name:          n.Name,
		Status:        n.Status,
		AdvertiseHost: n.AdvertiseHost,
		JoinToken:     token,
		InProgress:    n.InProgress,
		CreatedAt:     n.CreatedAt.UTC(),
		UpdatedAt:     n.UpdatedAt.UTC(),
	}
	if n.HasLastSeen {
		out.LastSeenAt = n.LastSeenAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	if s.Nodes == nil {
		writeError(w, http.StatusInternalServerError, "nodes are not configured")
		return
	}
	if _, ok := ActorFrom(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req createNodeRequest
	if !decodeJSON(r, w, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	n, token, err := s.Nodes.CreateNode(ctx, req.Name, req.AdvertiseHost)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "node name") || strings.Contains(err.Error(), "advertise_host") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger().Error("create node failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create node")
		return
	}
	s.audit(r, "", "node.create", "node", n.ID, map[string]string{"name": n.Name})
	writeJSON(w, http.StatusCreated, nodeResponseFrom(n, token))
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	if s.Nodes == nil {
		writeError(w, http.StatusInternalServerError, "nodes are not configured")
		return
	}
	if _, ok := ActorFrom(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	nodes, err := s.Nodes.ListNodes(ctx)
	if err != nil {
		s.logger().Error("list nodes failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list nodes")
		return
	}
	out := make([]nodeResponse, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeResponseFrom(n, ""))
	}
	writeJSON(w, http.StatusOK, out)
}

// enqueuePlaced RPUSH's onto a placed node's list. When Placer or Sink
// is nil (unit tests), the job stays on the shared Jobs queue.
func (s *Server) enqueuePlaced(ctx context.Context, job queue.Job) (nodeID string, err error) {
	if s.Placer == nil || s.Sink == nil {
		return "", s.Jobs.Enqueue(ctx, job)
	}
	node, err := s.Placer.Place(ctx)
	if err != nil {
		return "", err
	}
	if err := s.Sink.EnqueueOn(ctx, node.ID, job); err != nil {
		return "", err
	}
	return node.ID, nil
}

func (s *Server) placeDeployment(ctx context.Context, d store.Deployment) (store.Deployment, error) {
	if s.Placer == nil || s.Sink == nil || s.Nodes == nil {
		return d, nil
	}
	node, err := s.Placer.Place(ctx)
	if err != nil {
		return d, err
	}
	if err := s.Nodes.SetDeploymentNode(ctx, d.ID, node.ID); err != nil {
		return d, err
	}
	d.NodeID = node.ID
	d.NodeAdvertiseHost = node.AdvertiseHost
	return d, nil
}

func (s *Server) failQueuedDeploy(ctx context.Context, d store.Deployment, msg string) {
	d.Status = store.StatusFailed
	d.FailedStage = string(store.StatusQueued)
	d.ErrorMessage = store.SanitizeErrorMessage(msg)
	if s.Deployments != nil {
		_ = s.Deployments.UpdateDeployment(ctx, d)
	}
}
