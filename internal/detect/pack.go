package detect

import (
	"regexp"
	"strings"
)

// Pack is one deployment strategy.
//
// Match is file presence / shallow manifest checks only. Analyze may
// read those files and must still not exec. Generated Dockerfiles are
// Forge-owned. User-controlled strings must not be interpolated unless
// they pass safeIdent.
type Pack interface {
	ID() Kind
	Match(idx fileIndex) bool
	Analyze(idx fileIndex) (Result, error)
}

// packs is the priority table. Dockerfile always wins. After that,
// language manifests that often coexist with a frontend package.json
// (Rails, Laravel, Go) are listed before Node so a Gemfile+package.json
// repo is Ruby, not Node. Node is last among application packs.
func packs() []Pack {
	return []Pack{
		dockerfilePack{},
		goPack{},
		rubyPack{},
		phpPack{},
		javaPack{},
		rustPack{},
		dotnetPack{},
		pythonPack{},
		nodePack{},
		staticPack{},
		ccPack{},
	}
}

var safeIdent = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

func ident(s string) string {
	s = strings.TrimSpace(s)
	if safeIdent.MatchString(s) {
		return s
	}
	return ""
}

func baseResult(id Kind, language, manifest, reason string) Result {
	return Result{
		Kind:     id,
		Language: language,
		Runtime:  RuntimeServer,
		Source: DetectionSource{
			Pack:     string(id),
			Manifest: manifest,
			Reason:   reason,
		},
	}
}
