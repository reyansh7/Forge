package queue

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"
)

// Redis is a JobQueue backed by a Redis LIST.
//
// RPUSH adds to the tail; BLPOP removes from the head — FIFO.
// BLPOP with a 1-second timeout is a blocking wait, not a busy loop.
// On timeout we return ErrEmpty so the worker can check context.Cancel.
//
// Limitation: after BLPOP the job is gone from Redis even if the worker
// crashes before Handle returns. There is no ack, retry, or dead-letter
// in this increment. Do not treat this as durable job state.
type Redis struct {
	addr     string
	password string
	key      string
}

// NewRedis parses a redis:// URL. rediss:// (TLS) is still out of scope.
// key is the LIST name; tests pass a unique suffix so they do not steal
// live forge:jobs traffic.
func NewRedis(rawURL, key string) (*Redis, error) {
	if key == "" {
		key = DefaultKey
	}
	addr, password, err := parseRedisURL(rawURL)
	if err != nil {
		return nil, err
	}
	return &Redis{addr: addr, password: password, key: key}, nil
}

func parseRedisURL(rawURL string) (addr, password string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("parse redis url: %w", err)
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return "", "", fmt.Errorf("redis url scheme must be redis or rediss, got %q", u.Scheme)
	}
	if u.Scheme == "rediss" {
		return "", "", fmt.Errorf("rediss (TLS) is not implemented in increment 0.3")
	}
	host := u.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	port := u.Port()
	if port == "" {
		port = "6379"
	}
	password, _ = u.User.Password()
	return net.JoinHostPort(host, port), password, nil
}

// Ping is a startup check. It does not log the URL or password.
func (q *Redis) Ping(ctx context.Context) error {
	conn, br, err := q.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	v, err := q.command(br, conn, "PING")
	if err != nil {
		return err
	}
	if v.kind == respError {
		return fmt.Errorf("redis PING: %s", v.str)
	}
	if v.kind != respSimple || v.str != "PONG" {
		return fmt.Errorf("redis PING unexpected reply")
	}
	return nil
}

// Enqueue RPUSHes the JSON job. A dial/write failure is returned to HTTP as 503.
func (q *Redis) Enqueue(ctx context.Context, job Job) error {
	raw, err := job.Marshal()
	if err != nil {
		return err
	}
	conn, br, err := q.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	v, err := q.command(br, conn, "RPUSH", q.key, string(raw))
	if err != nil {
		return fmt.Errorf("redis RPUSH: %w", err)
	}
	if v.kind == respError {
		return fmt.Errorf("redis RPUSH: %s", v.str)
	}
	return nil
}

// Dequeue waits until a job is available or ctx is cancelled.
func (q *Redis) Dequeue(ctx context.Context) (Job, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Job{}, err
		}
		job, err := q.blpop(ctx)
		if err == nil {
			return job, nil
		}
		if errors.Is(err, ErrEmpty) {
			continue
		}
		return Job{}, err
	}
}

func (q *Redis) blpop(ctx context.Context) (Job, error) {
	conn, br, err := q.dial(ctx)
	if err != nil {
		return Job{}, err
	}
	defer conn.Close()

	// Close the socket if the worker is cancelled mid-BLPOP so shutdown
	// does not wait for the 1s Redis timeout (or a wedged peer).
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	// 1-second block: long enough to avoid spinning, short enough to
	// notice Ctrl+C without waiting on a forever BLPOP.
	v, err := q.command(br, conn, "BLPOP", q.key, "1")
	if err != nil {
		return Job{}, fmt.Errorf("redis BLPOP: %w", err)
	}
	if v.kind == respError {
		return Job{}, fmt.Errorf("redis BLPOP: %s", v.str)
	}
	if v.kind == respNull {
		return Job{}, ErrEmpty
	}
	if v.kind != respArray || len(v.items) != 2 {
		return Job{}, fmt.Errorf("redis BLPOP: unexpected reply")
	}
	payload := v.items[1]
	if payload.kind != respBulk {
		return Job{}, fmt.Errorf("redis BLPOP: expected bulk job")
	}
	return ParseJob([]byte(payload.str))
}

func (q *Redis) dial(ctx context.Context) (net.Conn, *bufio.Reader, error) {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", q.addr)
	if err != nil {
		return nil, nil, fmt.Errorf("redis dial: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		// BLPOP 1s plus protocol overhead.
		deadline = time.Now().Add(3 * time.Second)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	if q.password != "" {
		v, err := q.command(br, conn, "AUTH", q.password)
		if err != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("redis AUTH: %w", err)
		}
		if v.kind == respError {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("redis AUTH: %s", v.str)
		}
	}
	return conn, br, nil
}

func (q *Redis) command(br *bufio.Reader, w net.Conn, args ...string) (respValue, error) {
	if err := writeRESP(w, args...); err != nil {
		return respValue{}, err
	}
	return readRESP(br)
}
