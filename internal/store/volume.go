package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ApplicationVolume is an operator declaration: this app's next
// container should keep a Docker named volume at MountPath.
//
// What it is: persistent disk that survives Stop + Deploy (the
// container is replaced; the volume is not). Why: SQLite files,
// uploads, and caches must not vanish when the operator redeploys.
// How: the worker `docker volume create`s a Forge-owned name and
// passes `-v name:path`. The left side is never a host path.
// Production and preview get different Docker names so a trial
// SQLite cannot clobber live files. Rolling deploys reuse the
// same env-scoped name; docker stop/rm never deletes the volume.
//
// Called by: HTTP (CRUD) and the worker (list at provision).
// Security: another application's volume cannot be selected by id.
// Docker names are computed from this app's UUID and environment.
// Host binds, Docker socket paths, and OS directories are rejected
// here and again in runtime before argv is built.
//
// Deferred: size quotas (local driver has no portable cap),
// read-only mounts, sharing one volume across two apps, overlay
// storage that follows a live migrate (there is no live migrate).
type ApplicationVolume struct {
	ID            string
	ApplicationID string
	Name          string
	MountPath     string
}

const maxApplicationVolumes = 8

// DockerVolumeName is the only legal left-hand side of `docker run -v`.
//
// Production keeps forgevol_<32 hex>_<name> so Phase 7.2 data on an
// existing node is not orphaned. Preview uses
// forgevol_<32 hex>_preview_<name> so the two environments never
// share a Docker volume. Compose volumes (forge_postgres_…) cannot
// match this prefix. Operators never choose the prefix.
func DockerVolumeName(applicationID, name, environment string) (string, error) {
	id, err := ParseUUID(applicationID)
	if err != nil {
		return "", err
	}
	name, err = ValidateVolumeName(name)
	if err != nil {
		return "", err
	}
	env, err := ParseEnvironment(environment)
	if err != nil {
		return "", err
	}
	hex := strings.ReplaceAll(id, "-", "")
	if env == EnvPreview {
		return "forgevol_" + hex + "_preview_" + name, nil
	}
	return "forgevol_" + hex + "_" + name, nil
}

// DockerVolumeNames is production then preview for one declaration.
// Operator delete and application delete must remove both so a
// leftover preview volume cannot be mounted by a later trial deploy
// after the row is gone. Deploy/restart/rollback never call this.
func DockerVolumeNames(applicationID, name string) ([]string, error) {
	prod, err := DockerVolumeName(applicationID, name, EnvProduction)
	if err != nil {
		return nil, err
	}
	prev, err := DockerVolumeName(applicationID, name, EnvPreview)
	if err != nil {
		return nil, err
	}
	return []string{prod, prev}, nil
}

// volumeNamePattern is a Docker-safe label (1–32 chars). It is
// interpolated into `forgevol_<uuid>_<name>`, so spaces, slashes,
// and ".." are rejected.
var volumeNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidateVolumeName is a DNS-ish label used only as the suffix of a
// Forge-owned Docker volume name. Operators never choose the full name.
func ValidateVolumeName(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", fmt.Errorf("volume name is required")
	}
	if utf8.RuneCountInString(s) > 32 {
		return "", fmt.Errorf("volume name must be at most 32 characters")
	}
	if !volumeNamePattern.MatchString(s) {
		return "", fmt.Errorf("volume name must be a lowercase DNS label")
	}
	return s, nil
}

// ValidateMountPath is the container destination, never a host path.
//
// Absolute Unix path only. Bind-mount syntax (`/host:/dest`, `C:\…`)
// and traversal (`/data/../etc`) are rejected so a crafted row cannot
// become `-v /var/run/docker.sock:…` or hide `/etc`.
func ValidateMountPath(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("mount path is required")
	}
	if utf8.RuneCountInString(s) > 128 {
		return "", fmt.Errorf("mount path must be at most 128 characters")
	}
	if strings.ContainsAny(s, "\x00\r\n:\\") {
		return "", fmt.Errorf("mount path contains invalid characters")
	}
	if !strings.HasPrefix(s, "/") || s == "/" {
		return "", fmt.Errorf("mount path must be an absolute container path")
	}
	if strings.Contains(s, "//") || strings.Contains(s, "/./") || strings.HasSuffix(s, "/.") {
		return "", fmt.Errorf("mount path must be a clean container path")
	}
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("mount path must not contain ..")
	}
	cleaned := path.Clean(s)
	if cleaned != s {
		return "", fmt.Errorf("mount path must be a clean container path")
	}
	if forbiddenMount(cleaned) {
		return "", fmt.Errorf("mount path %q is reserved by the runtime", cleaned)
	}
	return cleaned, nil
}

func forbiddenMount(p string) bool {
	if p == "/" || p == "/tmp" || p == "/app" {
		// /tmp is already a tmpfs. /app is the image working tree —
		// mounting over it hides the built program.
		return true
	}
	prefixes := []string{
		"/etc", "/proc", "/sys", "/dev", "/boot",
		"/lib", "/lib64", "/usr", "/bin", "/sbin",
		"/var/run", "/run",
		"/root", "/home",
		"/.docker", "/.ssh", "/.gnupg", "/.forge",
	}
	for _, pre := range prefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

func mountsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (p *Postgres) CreateApplicationVolume(ctx context.Context, applicationID, name, mountPath string) (ApplicationVolume, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return ApplicationVolume{}, err
	}
	name, err = ValidateVolumeName(name)
	if err != nil {
		return ApplicationVolume{}, err
	}
	mountPath, err = ValidateMountPath(mountPath)
	if err != nil {
		return ApplicationVolume{}, err
	}
	if _, err := p.GetApplication(ctx, applicationID); err != nil {
		return ApplicationVolume{}, err
	}
	existing, err := p.ListApplicationVolumes(ctx, applicationID)
	if err != nil {
		return ApplicationVolume{}, err
	}
	if len(existing) >= maxApplicationVolumes {
		return ApplicationVolume{}, fmt.Errorf("at most %d volumes per application", maxApplicationVolumes)
	}
	for _, v := range existing {
		if mountsOverlap(v.MountPath, mountPath) {
			return ApplicationVolume{}, fmt.Errorf("mount path overlaps %s", v.MountPath)
		}
	}

	var out ApplicationVolume
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO application_volumes (application_id, name, mount_path)
		VALUES ($1::uuid, $2, $3)
		RETURNING id::text, application_id::text, name, mount_path
	`, applicationID, name, mountPath).Scan(&out.ID, &out.ApplicationID, &out.Name, &out.MountPath)
	if err != nil {
		if isUniqueViolation(err) {
			return ApplicationVolume{}, fmt.Errorf("%w: volume name or mount path already exists", ErrConflict)
		}
		return ApplicationVolume{}, fmt.Errorf("create application volume: %w", err)
	}
	return out, nil
}

func (p *Postgres) ListApplicationVolumes(ctx context.Context, applicationID string) ([]ApplicationVolume, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id::text, application_id::text, name, mount_path
		FROM application_volumes
		WHERE application_id = $1::uuid
		ORDER BY name ASC
	`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list application volumes: %w", err)
	}
	defer rows.Close()
	out := make([]ApplicationVolume, 0)
	for rows.Next() {
		var v ApplicationVolume
		if err := rows.Scan(&v.ID, &v.ApplicationID, &v.Name, &v.MountPath); err != nil {
			return nil, fmt.Errorf("scan application volume: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteApplicationVolume(ctx context.Context, applicationID, volumeID string) error {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return err
	}
	volumeID, err = ParseUUID(volumeID)
	if err != nil {
		return err
	}
	res, err := p.db.ExecContext(ctx, `
		DELETE FROM application_volumes
		WHERE id = $1::uuid AND application_id = $2::uuid
	`, volumeID, applicationID)
	if err != nil {
		return fmt.Errorf("delete application volume: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) GetApplicationVolume(ctx context.Context, applicationID, volumeID string) (ApplicationVolume, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return ApplicationVolume{}, err
	}
	volumeID, err = ParseUUID(volumeID)
	if err != nil {
		return ApplicationVolume{}, err
	}
	var out ApplicationVolume
	err = p.db.QueryRowContext(ctx, `
		SELECT id::text, application_id::text, name, mount_path
		FROM application_volumes
		WHERE id = $1::uuid AND application_id = $2::uuid
	`, volumeID, applicationID).Scan(&out.ID, &out.ApplicationID, &out.Name, &out.MountPath)
	if errors.Is(err, sql.ErrNoRows) {
		return ApplicationVolume{}, ErrNotFound
	}
	if err != nil {
		return ApplicationVolume{}, fmt.Errorf("get application volume: %w", err)
	}
	return out, nil
}
