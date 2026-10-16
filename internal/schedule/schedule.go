// Package schedule picks which worker node should run the next job.
//
// What it is: placement. Why a package: ARCHITECTURE.md forbids
// embedding node selection inside HTTP handlers. The handler asks
// Place(); it does not count heartbeats or walk Redis keys.
//
// Called by: cmd/api (wired onto httpapi.Server.Placer).
// It calls: store.ListReadyNodes, which marks stale heartbeats dead
// first. A dead node is not a reason to drop PostgreSQL.
//
// Capacity is in-flight deployments already assigned to the node
// (queued through health_check). This is not Kubernetes and not
// live migration. Moving work is Stop + Deploy so Place can pick
// another ready node.
//
// Deferred: bin-packing, affinity, tenant network mesh, AWS.
package schedule

import (
	"context"
	"errors"
	"strings"

	"github.com/reyansh7/Forge/internal/config"
	"github.com/reyansh7/Forge/internal/store"
)

// ErrNoCapacity means every ready node is at MaxInflight, or none
// are ready. HTTP maps this to 409, not 500.
var ErrNoCapacity = errors.New("no ready worker node with capacity")

// NodeSource is the scheduler → PostgreSQL boundary.
// Tests inject a fake; production injects *store.Postgres.
type NodeSource interface {
	ListReadyNodes(ctx context.Context) ([]store.Node, error)
}

// Scheduler implements httpapi.Placer.
type Scheduler struct {
	Nodes       NodeSource
	MaxInflight int
}

func (s *Scheduler) cap() int {
	if s != nil && s.MaxInflight > 0 {
		return s.MaxInflight
	}
	return config.DefaultMaxInflightPerNode
}

// Place returns one ready node under the inflight cap.
//
// Invariant: the caller must persist deployments.node_id before
// RPUSH onto that node's list. Otherwise a restarting worker cannot
// abandon only its own interrupted rows.
func (s *Scheduler) Place(ctx context.Context) (store.Node, error) {
	return s.PlacePrefer(ctx, "")
}

// PlacePrefer is Place with a volume-affinity hint.
//
// Docker named volumes live on the node that created them. If an
// application has volumes, HTTP passes the last node_id so the next
// deploy can see the same disk. If that node is dead or at cap, we
// fall back to least-loaded — the new node gets an empty volume.
// That is not live migrate; it is documented data locality.
func (s *Scheduler) PlacePrefer(ctx context.Context, preferNodeID string) (store.Node, error) {
	if s == nil || s.Nodes == nil {
		return store.Node{}, ErrNoCapacity
	}
	ready, err := s.Nodes.ListReadyNodes(ctx)
	if err != nil {
		return store.Node{}, err
	}
	limit := s.cap()
	preferNodeID = strings.TrimSpace(preferNodeID)
	var best *store.Node
	for i := range ready {
		n := ready[i]
		if n.InProgress >= limit {
			continue
		}
		if preferNodeID != "" && n.ID == preferNodeID {
			return n, nil
		}
		if best == nil || n.InProgress < best.InProgress {
			cp := n
			best = &cp
		}
	}
	if best == nil {
		return store.Node{}, ErrNoCapacity
	}
	return *best, nil
}
