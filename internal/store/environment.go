package store

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Environment is which slot a deployment occupies on an application.
//
// What it is: preview vs production as first-class state, not a second
// application. Why: operators need an isolated Host and container for
// a trial deploy without stopping production. How: deployments.environment
// plus one LIVE row per (application_id, environment). Production keeps
// {slug}.localhost. Preview uses {slug}-preview.localhost.
//
// Service links (7.1) still inject the production Host URL. Preview
// does not steal that address. Persistent volumes (7.5) use a
// distinct Docker name per environment so preview disk is not
// production disk. Custom domains are not this increment.
const (
	EnvProduction = "production"
	EnvPreview    = "preview"
)

const previewHostSuffix = "-preview"

// ParseEnvironment accepts production, preview, or empty (production).
func ParseEnvironment(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return EnvProduction, nil
	}
	if s != EnvProduction && s != EnvPreview {
		return "", fmt.Errorf("environment must be production or preview")
	}
	return s, nil
}

// SameEnvironment treats empty as production so pre-7.3 test rows match.
func SameEnvironment(a, b string) bool {
	aa, _ := ParseEnvironment(a)
	bb, _ := ParseEnvironment(b)
	return aa == bb
}

// PreviewLocalHost is the Host slug for a preview deployment.
//
// It is derived from the production slug so Caddy matchers stay unique
// without a second applications.local_host column. Operators cannot
// set local_host to this suffix (ValidateLocalHost). Long slugs are
// truncated so the result remains a DNS label (≤63).
func PreviewLocalHost(slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return ""
	}
	if strings.HasSuffix(slug, previewHostSuffix) {
		return slug
	}
	max := maxLocalHostLen - len(previewHostSuffix)
	if max < 1 {
		return ""
	}
	if utf8.RuneCountInString(slug) > max {
		runes := []rune(slug)
		slug = strings.TrimRight(string(runes[:max]), "-")
	}
	out := slug + previewHostSuffix
	if !localHostPattern.MatchString(out) {
		return ""
	}
	return out
}

// RouteLocalHost is the Caddy Host label for a live deployment.
func RouteLocalHost(environment, productionSlug string) string {
	env, err := ParseEnvironment(environment)
	if err != nil || productionSlug == "" {
		return ""
	}
	if env == EnvPreview {
		return PreviewLocalHost(productionSlug)
	}
	return productionSlug
}
