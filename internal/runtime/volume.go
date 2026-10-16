package runtime

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// VolumeMount is one Docker named volume attached at docker run.
//
// Name is the Docker volume (forgevol_…). Path is the container
// destination. Neither side may be a host filesystem path. The worker
// maps store rows here after validation; Run validates again so a
// corrupted row cannot become `-v /var/run/docker.sock:…`.
// Preview names include `_preview_` after the app hex so they cannot
// resolve to the production volume of the same operator label.
type VolumeMount struct {
	Name string
	Path string
}

// dockerVolumeNamePattern is the only left-hand side HostDocker will
// put after `-v`. Production: forgevol_<hex>_<label>. Preview:
// forgevol_<hex>_preview_<label>. Optional `preview_` is a Forge
// token, not an operator-chosen underscore (volume labels are DNS).
var dockerVolumeNamePattern = regexp.MustCompile(`^forgevol_[0-9a-f]{32}_(preview_)?[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

func validateVolumeMount(v VolumeMount) error {
	if !dockerVolumeNamePattern.MatchString(strings.TrimSpace(v.Name)) {
		return fmt.Errorf("invalid volume name")
	}
	if strings.ContainsAny(v.Name, "/\\:") {
		return fmt.Errorf("invalid volume name")
	}
	p := strings.TrimSpace(v.Path)
	if p == "" || !strings.HasPrefix(p, "/") || p == "/" {
		return fmt.Errorf("invalid mount path")
	}
	if strings.ContainsAny(p, "\x00\r\n:\\") || strings.Contains(p, "..") {
		return fmt.Errorf("invalid mount path")
	}
	if path.Clean(p) != p {
		return fmt.Errorf("invalid mount path")
	}
	if forbiddenRuntimeMount(p) {
		return fmt.Errorf("mount path %q is reserved by the runtime", p)
	}
	return nil
}

func forbiddenRuntimeMount(p string) bool {
	if p == "/tmp" || p == "/app" {
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
