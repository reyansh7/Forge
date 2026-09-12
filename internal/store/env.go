package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// EnvVar is one operator-supplied environment binding for an application.
//
// Values are persisted in PostgreSQL. Phase 5 seals them with AES-GCM
// when Postgres.crypter is set. A SELECT of the table then shows
// enc:v1:... not the plaintext. Anyone with the data key or a live
// process can still decrypt — this is encrypted-at-rest, not a vault.
// Never log Value. Audit events may record the key name only.
type EnvVar struct {
	Key   string
	Value string
}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	maxEnvVars     = 32
	maxEnvValueLen = 4096
)

// ValidateEnvKey rejects keys the control plane owns or that could be
// mistaken for flags. PORT is Forge-owned (container listen port).
func ValidateEnvKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("env key is required")
	}
	if !envKeyPattern.MatchString(key) {
		return fmt.Errorf("env key must be a shell identifier")
	}
	if strings.EqualFold(key, "PORT") || strings.EqualFold(key, "HOST") {
		return fmt.Errorf("%s is reserved by Forge", strings.ToUpper(key))
	}
	if strings.HasPrefix(strings.ToUpper(key), "FORGE_") {
		return fmt.Errorf("FORGE_ keys are reserved")
	}
	return nil
}

func ValidateEnvValue(value string) error {
	if utf8.RuneCountInString(value) > maxEnvValueLen {
		return fmt.Errorf("env value must be at most %d characters", maxEnvValueLen)
	}
	if containsCtl(value) && value != "" {
		// Allow empty; reject CR/LF/NUL so values cannot split docker -e.
		return fmt.Errorf("env value contains invalid characters")
	}
	return nil
}

func (p *Postgres) ListEnvVars(ctx context.Context, applicationID string) ([]EnvVar, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT key, value FROM application_env_vars
		WHERE application_id = $1::uuid
		ORDER BY key ASC
	`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list env: %w", err)
	}
	defer rows.Close()
	out := make([]EnvVar, 0)
	for rows.Next() {
		var item EnvVar
		if err := rows.Scan(&item.Key, &item.Value); err != nil {
			return nil, fmt.Errorf("scan env: %w", err)
		}
		plain, err := p.crypter.Open(item.Value)
		if err != nil {
			return nil, fmt.Errorf("open env %s: %w", item.Key, err)
		}
		item.Value = plain
		out = append(out, item)
	}
	return out, rows.Err()
}

func (p *Postgres) PutEnvVar(ctx context.Context, applicationID, key, value string) error {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return err
	}
	if err := ValidateEnvKey(key); err != nil {
		return err
	}
	if err := ValidateEnvValue(value); err != nil {
		return err
	}
	key = strings.TrimSpace(key)

	var n int
	if err := p.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM application_env_vars WHERE application_id = $1::uuid
	`, applicationID).Scan(&n); err != nil {
		return fmt.Errorf("count env: %w", err)
	}
	var exists bool
	if err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM application_env_vars
			WHERE application_id = $1::uuid AND key = $2
		)
	`, applicationID, key).Scan(&exists); err != nil {
		return fmt.Errorf("check env: %w", err)
	}
	if !exists && n >= maxEnvVars {
		return fmt.Errorf("at most %d environment variables per application", maxEnvVars)
	}

	stored, err := p.crypter.Seal(value)
	if err != nil {
		return fmt.Errorf("seal env: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO application_env_vars (application_id, key, value)
		VALUES ($1::uuid, $2, $3)
		ON CONFLICT (application_id, key)
		DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, applicationID, key, stored)
	if err != nil {
		return fmt.Errorf("put env: %w", err)
	}
	return nil
}

func (p *Postgres) DeleteEnvVar(ctx context.Context, applicationID, key string) error {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	res, err := p.db.ExecContext(ctx, `
		DELETE FROM application_env_vars
		WHERE application_id = $1::uuid AND key = $2
	`, applicationID, key)
	if err != nil {
		return fmt.Errorf("delete env: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceEnvVars atomically replaces the application's env set.
//
// The dashboard "paste KEY=VALUE" editor sends the whole map. Doing
// DELETE+INSERT in one transaction means a failed validation cannot
// leave a half-cleared set. Values are still not a secret manager.
func (p *Postgres) ReplaceEnvVars(ctx context.Context, applicationID string, vars []EnvVar) error {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return err
	}
	if len(vars) > maxEnvVars {
		return fmt.Errorf("at most %d environment variables per application", maxEnvVars)
	}
	seen := make(map[string]struct{}, len(vars))
	clean := make([]EnvVar, 0, len(vars))
	for _, ev := range vars {
		if err := ValidateEnvKey(ev.Key); err != nil {
			return err
		}
		if err := ValidateEnvValue(ev.Value); err != nil {
			return err
		}
		key := strings.TrimSpace(ev.Key)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("duplicate env key %s", key)
		}
		seen[key] = struct{}{}
		clean = append(clean, EnvVar{Key: key, Value: ev.Value})
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace env: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM application_env_vars WHERE application_id = $1::uuid
	`, applicationID); err != nil {
		return fmt.Errorf("clear env: %w", err)
	}
	for _, ev := range clean {
		stored, err := p.crypter.Seal(ev.Value)
		if err != nil {
			return fmt.Errorf("seal env: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO application_env_vars (application_id, key, value)
			VALUES ($1::uuid, $2, $3)
		`, applicationID, ev.Key, stored); err != nil {
			return fmt.Errorf("insert env: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace env: %w", err)
	}
	return nil
}
