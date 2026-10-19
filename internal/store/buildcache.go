package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
)

const buildCacheKeep = 8

var (
	cacheKeyPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	cacheImagePattern = regexp.MustCompile(`^forge-app-[0-9a-f]{32}$`)
	cacheIDPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// LookupBuildCache returns the image indexed for this application and
// key. ok is false when no row exists. The caller must still inspect
// the image and require image_id to match before reusing it. A row is
// not proof the image is still on the machine or that the app is healthy.
func (p *Postgres) LookupBuildCache(ctx context.Context, applicationID, cacheKey string) (imageName, imageID string, ok bool, err error) {
	if !cacheKeyPattern.MatchString(cacheKey) {
		return "", "", false, fmt.Errorf("build cache: invalid key")
	}
	err = p.db.QueryRowContext(ctx, `
		SELECT image_name, image_id
		FROM build_cache
		WHERE application_id = $1 AND cache_key = $2
	`, applicationID, cacheKey).Scan(&imageName, &imageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("build cache lookup: %w", err)
	}
	return imageName, imageID, true, nil
}

// RememberBuildCache records an image after the worker has inspected it.
//
// ON CONFLICT DO NOTHING keeps the first validated image for that key.
// A second racing build does not replace it. The loser's own image tag
// remains for that deployment; this method does not delete images.
//
// Rows beyond the newest eight are removed only when no deployment of
// this application still names that image. Referenced rollback images
// stay indexed. The DELETE removes index rows, never Docker images.
func (p *Postgres) RememberBuildCache(ctx context.Context, applicationID, cacheKey, imageName, imageID string) error {
	if !cacheKeyPattern.MatchString(cacheKey) || !cacheImagePattern.MatchString(imageName) || !cacheIDPattern.MatchString(imageID) {
		return fmt.Errorf("build cache: refused invalid record")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("build cache begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO build_cache (application_id, cache_key, image_name, image_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (application_id, cache_key) DO NOTHING
	`, applicationID, cacheKey, imageName, imageID); err != nil {
		return fmt.Errorf("build cache insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM build_cache
		WHERE application_id = $1
		  AND cache_key NOT IN (
		    SELECT cache_key FROM build_cache
		    WHERE application_id = $1
		    ORDER BY created_at DESC
		    LIMIT $2
		  )
		  AND image_name NOT IN (
		    SELECT image_name FROM deployments
		    WHERE application_id = $1 AND image_name <> ''
		  )
	`, applicationID, buildCacheKeep); err != nil {
		return fmt.Errorf("build cache prune: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("build cache commit: %w", err)
	}
	return nil
}

// ForgetBuildCache drops an index row whose image is missing or whose
// ID no longer matches. It does not run docker rmi.
func (p *Postgres) ForgetBuildCache(ctx context.Context, applicationID, cacheKey string) error {
	if !cacheKeyPattern.MatchString(cacheKey) {
		return fmt.Errorf("build cache: invalid key")
	}
	if _, err := p.db.ExecContext(ctx, `
		DELETE FROM build_cache WHERE application_id = $1 AND cache_key = $2
	`, applicationID, cacheKey); err != nil {
		return fmt.Errorf("build cache forget: %w", err)
	}
	return nil
}
