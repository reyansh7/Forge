// Package store talks to control-plane data stores (Postgres, Redis).
//
// Postgres holds durable Forge state (increment 0.2: projects table).
// Redis is still liveness-only in this phase (no queue).
// These clients are for Forge's own state — not databases that user
// apps will attach later. This package must never execute user application
// code or log connection secrets.
package store

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Pinger is a store that can prove it is reachable and be closed.
// io.Closer is embedded so callers can defer p.Close() on the interface.
type Pinger interface {
	Ping(ctx context.Context) error
	io.Closer
}

// RedisPinger sends a RESP PING over TCP.
//
// RESP is Redis's text protocol. Increment 0.1 only needs liveness, so we
// speak PING/PONG (and AUTH if a password is in the URL) instead of adding
// a Redis client library. TLS (rediss://) is deferred.
type RedisPinger struct {
	addr     string
	password string
}

// NewRedisPinger parses a redis:// URL into host:port and optional password.
func NewRedisPinger(rawURL string) (*RedisPinger, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return nil, fmt.Errorf("redis url scheme must be redis or rediss, got %q", u.Scheme)
	}
	if u.Scheme == "rediss" {
		return nil, fmt.Errorf("rediss (TLS) is not implemented in increment 0.1")
	}

	host := u.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	port := u.Port()
	if port == "" {
		port = "6379" // Redis default; matches Compose
	}

	password, _ := u.User.Password()
	return &RedisPinger{
		// JoinHostPort handles IPv6 brackets correctly (unlike host+":"+port).
		addr:     net.JoinHostPort(host, port),
		password: password,
	}, nil
}

// Ping dials Redis and requires a PONG. It does not log the URL or password.
func (r *RedisPinger) Ping(ctx context.Context) error {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", r.addr)
	if err != nil {
		return fmt.Errorf("redis dial: %w", err)
	}
	defer conn.Close()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Second)
	}
	// SetDeadline covers both read and write so a silent peer cannot hang us.
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}

	if r.password != "" {
		if err := writeRESP(conn, "AUTH", r.password); err != nil {
			return err
		}
		if _, err := readRESPLine(conn); err != nil {
			return fmt.Errorf("redis AUTH: %w", err)
		}
	}

	if err := writeRESP(conn, "PING"); err != nil {
		return err
	}
	line, err := readRESPLine(conn)
	if err != nil {
		return fmt.Errorf("redis PING: %w", err)
	}
	if !strings.Contains(strings.ToUpper(line), "PONG") {
		return fmt.Errorf("redis PING unexpected reply")
	}
	return nil
}

// Close is a no-op: we do not hold a persistent Redis connection in 0.1.
func (r *RedisPinger) Close() error { return nil }

// writeRESP encodes a command as a RESP array of bulk strings.
// Example PING: *1\r\n$4\r\nPING\r\n
func writeRESP(w io.Writer, args ...string) error {
	var b strings.Builder
	b.WriteByte('*')
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, arg := range args {
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(len(arg)))
		b.WriteString("\r\n")
		b.WriteString(arg)
		b.WriteString("\r\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// readRESPLine reads one socket chunk. Good enough for a short +PONG reply;
// a full RESP parser is not justified until we need bulk replies.
func readRESPLine(r io.Reader) (string, error) {
	buf := make([]byte, 256)
	n, err := r.Read(buf)
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}
