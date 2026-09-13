package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode"
)

// Node is one worker/runtime machine that can run docker build/run.
//
// The control plane (API + Postgres + Redis + Caddy) stays a single
// logical process. Nodes are extra workers. Join is authenticated by a
// one-time token whose SHA-256 we store. Workloads on a node still
// cannot use the Docker socket or reach control-plane Redis/Postgres
// unless the operator published those sockets on that machine.
type Node struct {
	ID            string
	Name          string
	Status        string
	AdvertiseHost string
	LastSeenAt    time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	InProgress    int
	HasLastSeen   bool
}

const (
	NodeReady    = "ready"
	NodeDraining = "draining"
	NodeDead     = "dead"

	DefaultNodeName     = "local"
	NodeStaleAfter      = 45 * time.Second
	NodeHeartbeatEvery  = 15 * time.Second
	maxAdvertiseHostLen = 253
)

// ValidateNodeName is a DNS label so it can appear in logs and queue keys
// without Redis-key injection. Same shape as local_host slugs.
func ValidateNodeName(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", fmt.Errorf("node name is required")
	}
	if len(s) > 63 {
		return "", fmt.Errorf("node name must be at most 63 characters")
	}
	for i, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok || r > unicode.MaxASCII {
			return "", fmt.Errorf("node name must be a lowercase DNS label")
		}
		if (i == 0 || i == len(s)-1) && r == '-' {
			return "", fmt.Errorf("node name must be a lowercase DNS label")
		}
	}
	return s, nil
}

// ValidateAdvertiseHost is how Caddy reaches published ports on this
// node (hostname or IP, no scheme, no port). Empty means "use the
// process FORGE_CADDY_UPSTREAM_HOST" — correct for a worker on the
// same Docker host as Caddy.
func ValidateAdvertiseHost(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if len(s) > maxAdvertiseHostLen {
		return "", fmt.Errorf("advertise_host is too long")
	}
	if strings.Contains(s, "://") || strings.ContainsAny(s, " /\\@\r\n\x00") {
		return "", fmt.Errorf("advertise_host must be a hostname or IP")
	}
	if _, port, err := net.SplitHostPort(s); err == nil && port != "" {
		return "", fmt.Errorf("advertise_host must not include a port")
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() {
			// Loopback is allowed: a second worker on the same machine
			// publishes on 127.0.0.1 and Caddy uses host.docker.internal
			// unless the operator sets a LAN IP. Rejecting 127.0.0.1
			// would break the local two-process lab. Unspecified is not
			// a destination Caddy can route to.
			if ip.IsUnspecified() || ip.IsMulticast() {
				return "", fmt.Errorf("advertise_host is not a usable address")
			}
		}
		return s, nil
	}
	lower := strings.ToLower(s)
	for _, label := range strings.Split(lower, ".") {
		if label == "" {
			return "", fmt.Errorf("advertise_host must be a hostname or IP")
		}
		for i, r := range label {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
			if !ok || ((i == 0 || i == len(label)-1) && r == '-') {
				return "", fmt.Errorf("advertise_host must be a hostname or IP")
			}
		}
	}
	return lower, nil
}

// CreateNode inserts a ready node and returns the raw join token once.
func (p *Postgres) CreateNode(ctx context.Context, name, advertiseHost string) (Node, string, error) {
	name, err := ValidateNodeName(name)
	if err != nil {
		return Node{}, "", err
	}
	host, err := ValidateAdvertiseHost(advertiseHost)
	if err != nil {
		return Node{}, "", err
	}
	raw, hash, err := NewSessionToken()
	if err != nil {
		return Node{}, "", err
	}
	n, err := scanNode(p.db.QueryRowContext(ctx, `
		INSERT INTO nodes (name, token_hash, status, advertise_host, last_seen_at)
		VALUES ($1, $2, $3, $4, NULL)
		RETURNING `+nodeReturning, name, hash, NodeReady, host))
	if err != nil {
		if isUniqueViolation(err) {
			return Node{}, "", fmt.Errorf("%w: node name is already taken", ErrConflict)
		}
		return Node{}, "", fmt.Errorf("create node: %w", err)
	}
	return n, raw, nil
}

// EnsureLocalNode creates the default single-machine node if missing.
//
// The first worker on a laptop should not require POST /nodes. The
// token is discarded: claiming "local" is allowed without a token.
// A second named node still needs an operator-issued token.
func (p *Postgres) EnsureLocalNode(ctx context.Context) (Node, error) {
	existing, err := p.GetNodeByName(ctx, DefaultNodeName)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Node{}, err
	}
	n, _, err := p.CreateNode(ctx, DefaultNodeName, "")
	return n, err
}

func (p *Postgres) GetNode(ctx context.Context, id string) (Node, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return Node{}, err
	}
	return scanNode(p.db.QueryRowContext(ctx, `SELECT `+nodeReturning+` FROM nodes WHERE id = $1::uuid`, id))
}

func (p *Postgres) GetNodeByName(ctx context.Context, name string) (Node, error) {
	name, err := ValidateNodeName(name)
	if err != nil {
		return Node{}, err
	}
	return scanNode(p.db.QueryRowContext(ctx, `SELECT `+nodeReturning+` FROM nodes WHERE name = $1`, name))
}

// ClaimNode authenticates a worker. Token is required except for the
// reserved local node (empty token). Advertise host may be updated.
func (p *Postgres) ClaimNode(ctx context.Context, name, token, advertiseHost string) (Node, error) {
	name, err := ValidateNodeName(name)
	if err != nil {
		return Node{}, err
	}
	host, err := ValidateAdvertiseHost(advertiseHost)
	if err != nil {
		return Node{}, err
	}
	n, err := p.GetNodeByName(ctx, name)
	if err != nil {
		return Node{}, err
	}
	if name != DefaultNodeName || strings.TrimSpace(token) != "" {
		if strings.TrimSpace(token) == "" {
			return Node{}, fmt.Errorf("%w: node join token required", ErrUnauthorized)
		}
		want, err := p.nodeTokenHash(ctx, n.ID)
		if err != nil {
			return Node{}, err
		}
		if !TokenEqual(want, HashSessionToken(token)) {
			return Node{}, fmt.Errorf("%w: invalid node join token", ErrUnauthorized)
		}
	}
	return p.heartbeatNode(ctx, n.ID, host, NodeReady)
}

func (p *Postgres) nodeTokenHash(ctx context.Context, id string) (string, error) {
	var hash string
	err := p.db.QueryRowContext(ctx, `SELECT token_hash FROM nodes WHERE id = $1::uuid`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("node token: %w", err)
	}
	return hash, nil
}

func (p *Postgres) HeartbeatNode(ctx context.Context, id, advertiseHost string) (Node, error) {
	host, err := ValidateAdvertiseHost(advertiseHost)
	if err != nil {
		return Node{}, err
	}
	id, err = ParseUUID(id)
	if err != nil {
		return Node{}, err
	}
	return p.heartbeatNode(ctx, id, host, NodeReady)
}

func (p *Postgres) heartbeatNode(ctx context.Context, id, host, status string) (Node, error) {
	n, err := scanNode(p.db.QueryRowContext(ctx, `
		UPDATE nodes SET
			status = $2,
			advertise_host = CASE WHEN $3 <> '' THEN $3 ELSE advertise_host END,
			last_seen_at = now(),
			updated_at = now()
		WHERE id = $1::uuid
		RETURNING `+nodeReturning, id, status, host))
	if err != nil {
		return Node{}, fmt.Errorf("heartbeat node: %w", err)
	}
	return n, nil
}

// MarkStaleNodes flips ready nodes whose heartbeat is older than stale
// to dead. Postgres stays intact — this is not a restore or drop.
func (p *Postgres) MarkStaleNodes(ctx context.Context, stale time.Duration) (int, error) {
	if stale <= 0 {
		stale = NodeStaleAfter
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE nodes SET status = $1, updated_at = now()
		WHERE status = $2
		  AND last_seen_at IS NOT NULL
		  AND last_seen_at < now() - $3::interval
	`, NodeDead, NodeReady, fmt.Sprintf("%d seconds", int(stale.Seconds())))
	if err != nil {
		return 0, fmt.Errorf("mark stale nodes: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (p *Postgres) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+nodeReturning+`,
		       (SELECT COUNT(*) FROM deployments d
		         WHERE d.node_id = nodes.id
		           AND d.status IN ('queued','detecting','building','provisioning','deploying','health_check'))
		FROM nodes
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	out := make([]Node, 0)
	for rows.Next() {
		var n Node
		if err := scanNodeRow(rows, &n, true); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (p *Postgres) ListReadyNodes(ctx context.Context) ([]Node, error) {
	if _, err := p.MarkStaleNodes(ctx, NodeStaleAfter); err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+nodeReturning+`,
		       (SELECT COUNT(*) FROM deployments d
		         WHERE d.node_id = nodes.id
		           AND d.status IN ('queued','detecting','building','provisioning','deploying','health_check'))
		FROM nodes
		WHERE status = $1
		ORDER BY COALESCE(last_seen_at, created_at) ASC, name ASC
	`, NodeReady)
	if err != nil {
		return nil, fmt.Errorf("list ready nodes: %w", err)
	}
	defer rows.Close()
	out := make([]Node, 0)
	for rows.Next() {
		var n Node
		if err := scanNodeRow(rows, &n, true); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (p *Postgres) SetDeploymentNode(ctx context.Context, deploymentID, nodeID string) error {
	deploymentID, err := ParseUUID(deploymentID)
	if err != nil {
		return err
	}
	nodeID, err = ParseUUID(nodeID)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `
		UPDATE deployments SET node_id = $2::uuid, updated_at = now() WHERE id = $1::uuid
	`, deploymentID, nodeID)
	if err != nil {
		return fmt.Errorf("set deployment node: %w", err)
	}
	return nil
}

const nodeReturning = `
	id::text, name, status, advertise_host,
	COALESCE(last_seen_at, TIMESTAMP 'epoch'), created_at, updated_at,
	(last_seen_at IS NOT NULL)
`

func scanNode(row *sql.Row) (Node, error) {
	var n Node
	var seen time.Time
	var hasSeen bool
	err := row.Scan(&n.ID, &n.Name, &n.Status, &n.AdvertiseHost, &seen, &n.CreatedAt, &n.UpdatedAt, &hasSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("scan node: %w", err)
	}
	n.HasLastSeen = hasSeen
	if hasSeen {
		n.LastSeenAt = seen
	}
	return n, nil
}

func scanNodeRow(rows *sql.Rows, n *Node, withCount bool) error {
	var seen time.Time
	var hasSeen bool
	dest := []any{&n.ID, &n.Name, &n.Status, &n.AdvertiseHost, &seen, &n.CreatedAt, &n.UpdatedAt, &hasSeen}
	if withCount {
		dest = append(dest, &n.InProgress)
	}
	if err := rows.Scan(dest...); err != nil {
		return fmt.Errorf("scan node: %w", err)
	}
	n.HasLastSeen = hasSeen
	if hasSeen {
		n.LastSeenAt = seen
	}
	return nil
}
