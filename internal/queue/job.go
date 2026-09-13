// Package queue is the control-plane job transport.
//
// PostgreSQL remains the durable source of truth (projects, later
// deployments). Redis here is only a transient list: if Redis restarts
// without persistence, queued jobs vanish. That is accepted in increment
// 0.3 rather than pretending LIST+RPUSH is a durable workflow engine.
//
// HTTP handlers depend on JobQueue, not on RESP commands, so tests can
// inject Memory and a later backend can change without rewriting /jobs.
package queue

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// TypeExample is the increment 0.3 demonstration job (log and return).
const TypeExample = "example"

// TypeDeploy runs the Phase 0 local deployment pipeline.
//
// The API is the only caller that may enqueue this type. The payload is
// a control-plane deployment_id, never a shell command.
const TypeDeploy = "deploy"

// DefaultKey is the legacy single-list name. Phase 6 workers consume
// NodeKey(id) instead so two processes cannot steal each other's jobs.
const DefaultKey = "forge:jobs"

// nodeIDPattern matches a UUID so a node id cannot inject a second
// Redis key (CRLF, spaces, or "forge:jobs:other").
var nodeIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// NodeKey is the LIST one worker BLPOP's. The API RPUSH's here after
// Place() so a job cannot land on a node that never claimed it.
func NodeKey(nodeID string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(nodeID))
	if !nodeIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid node id")
	}
	return DefaultKey + ":" + id, nil
}

var (
	// ErrEmpty means a blocking pop timed out with nothing on the list.
	// The worker should loop, not treat this as a crash.
	ErrEmpty = errors.New("queue empty")

	// ErrMalformed means the LIST element was not a valid Job JSON.
	// The element is already popped; it is not replayed (LIST has no ack).
	ErrMalformed = errors.New("malformed job")

	// ErrUnknownType means Type is not in the worker's allowlist.
	ErrUnknownType = errors.New("unknown job type")
)

// Job is the JSON blob stored on the Redis LIST.
//
// Payload is opaque JSON. Increment 0.3's example handler ignores it and
// never treats it as a shell command. Do not log Payload — callers might
// later put tokens there by mistake.
type Job struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// NewID returns a 32-character hex id (16 random bytes).
// It is not a UUID; it only needs to be unique enough to correlate logs.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("job id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Marshal encodes the job for RPUSH. Empty payload becomes {}.
func (j Job) Marshal() ([]byte, error) {
	if err := j.validateForEnqueue(); err != nil {
		return nil, err
	}
	if len(j.Payload) == 0 {
		j.Payload = json.RawMessage(`{}`)
	}
	b, err := json.Marshal(j)
	if err != nil {
		return nil, fmt.Errorf("marshal job: %w", err)
	}
	return b, nil
}

// ParseJob decodes a LIST element. Unknown types are still returned as
// Job values so the worker can reject them after the pop (LIST semantics).
func ParseJob(raw []byte) (Job, error) {
	var j Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return Job{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	j.ID = strings.TrimSpace(j.ID)
	j.Type = strings.TrimSpace(j.Type)
	if j.ID == "" || j.Type == "" {
		return Job{}, fmt.Errorf("%w: id and type are required", ErrMalformed)
	}
	if len(j.Payload) == 0 {
		j.Payload = json.RawMessage(`{}`)
	}
	return j, nil
}

func (j Job) validateForEnqueue() error {
	if strings.TrimSpace(j.ID) == "" {
		return fmt.Errorf("job id is required")
	}
	if strings.TrimSpace(j.Type) == "" {
		return fmt.Errorf("job type is required")
	}
	return nil
}

// AllowedType reports whether the worker will accept this type.
// POST /jobs still only allows example; deploy is created via deployments.
func AllowedType(t string) bool {
	return t == TypeExample || t == TypeDeploy
}
