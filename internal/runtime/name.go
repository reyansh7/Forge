package runtime

import (
	"strings"
	"unicode"
)

// WorkloadName is the docker --name for a deployment.
//
// Format: <app-slug>-<short-id>. The slug comes from the Forge
// application name (not the GitHub repo). The short id is the first
// four hex digits of the deployment UUID so two deploys of the same
// app never share a name. Old rows without a stored name still resolve
// through LegacyContainerName so stop/logs keep working.
func WorkloadName(appName, deploymentID string) string {
	slug := SlugifyName(appName)
	short := ShortID(deploymentID)
	name := slug + "-" + short
	if err := validateContainerName(name); err != nil {
		return LegacyContainerName(deploymentID)
	}
	return name
}

// LegacyContainerName is the Phase 0 forge-run-<uuid> form.
func LegacyContainerName(deploymentID string) string {
	return "forge-run-" + strings.ReplaceAll(deploymentID, "-", "")
}

// ContainerName keeps the historical helper used by tests and older
// call sites. New control-plane code should use WorkloadName or
// ResolveContainerName so the dashboard name matches docker ps.
func ContainerName(deploymentID string) string {
	return LegacyContainerName(deploymentID)
}

// ResolveContainerName prefers the name persisted on the deployment
// row. Empty means a pre-migration container that was started as
// forge-run-<uuid>.
func ResolveContainerName(deploymentID, stored string) string {
	stored = strings.TrimSpace(stored)
	if stored != "" && validateContainerName(stored) == nil {
		return stored
	}
	return LegacyContainerName(deploymentID)
}

// ShortID is the first four hex characters of a UUID, without hyphens.
func ShortID(deploymentID string) string {
	hex := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(deploymentID), "-", ""))
	if len(hex) < 4 {
		return "0000"
	}
	for _, r := range hex[:4] {
		if !unicode.Is(unicode.Hex_Digit, r) {
			return "0000"
		}
	}
	return hex[:4]
}

// SlugifyName turns an application name into a Docker-safe label.
//
// "My Portfolio" → my-portfolio, "React/API" → react-api. Empty or
// punctuation-only names become "app" so --name never starts with a
// hyphen (Docker rejects that).
func SlugifyName(name string) string {
	var b strings.Builder
	lastDash := true
	n := 0
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
			n++
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
				n++
			}
		}
		if n >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "app"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "app-" + s
	}
	return s
}
