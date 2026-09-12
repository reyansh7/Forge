// Package backup dumps and restores the control-plane PostgreSQL.
//
// What: pg_dump / psql (or docker compose exec into the Compose
// postgres service). Why: Phase 5 disaster recovery. How: write SQL to
// a file the operator owns. Redis is not dumped — a restart drops the
// LIST; in-flight status is in SQL.
//
// Backups are secrets (they contain sealed env and user rows). Do not
// commit them. Do not log the database URL.
package backup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Dump writes a PostgreSQL custom-or-plain SQL backup to dest.
//
// Prefer host pg_dump when present. Otherwise `docker compose exec`
// against the local postgres service (loopback Compose).
func Dump(ctx context.Context, databaseURL, dest string) error {
	if strings.TrimSpace(dest) == "" {
		return fmt.Errorf("backup destination is required")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil && filepath.Dir(dest) != "." {
		return fmt.Errorf("backup dir: %w", err)
	}
	if _, err := exec.LookPath("pg_dump"); err == nil && strings.TrimSpace(databaseURL) != "" {
		cmd := exec.CommandContext(ctx, "pg_dump", "--no-owner", "--no-acl", databaseURL)
		return runWrite(cmd, dest)
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "exec", "-T", "postgres",
		"pg_dump", "-U", "forge", "--no-owner", "--no-acl", "forge")
	return runWrite(cmd, dest)
}

// Restore applies a SQL dump. This replaces objects in the target
// database. Run it only against a recovery instance.
func Restore(ctx context.Context, databaseURL, src string) error {
	if strings.TrimSpace(src) == "" {
		return fmt.Errorf("restore source is required")
	}
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer f.Close()
	if _, err := exec.LookPath("psql"); err == nil && strings.TrimSpace(databaseURL) != "" {
		cmd := exec.CommandContext(ctx, "psql", "--quiet", databaseURL)
		cmd.Stdin = f
		return run(cmd)
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "exec", "-T", "postgres",
		"psql", "-U", "forge", "-d", "forge", "-v", "ON_ERROR_STOP=1")
	cmd.Stdin = f
	return run(cmd)
}

// DefaultName is backups/forge-20060102-150405.sql under cwd.
func DefaultName() string {
	return filepath.Join("backups", "forge-"+time.Now().UTC().Format("20060102-150405")+".sql")
}

func runWrite(cmd *exec.Cmd, dest string) error {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dump failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := os.WriteFile(dest, stdout.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	if stdout.Len() < 32 {
		return fmt.Errorf("dump produced an empty backup")
	}
	return nil
}

func run(cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restore failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
