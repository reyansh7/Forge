package detect

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// startConfig is an operator-declared run command from the repo.
//
// forge.json is Forge's explicit mechanism. A Heroku-style Procfile
// `web:` line is the common ecosystem equivalent. Tokens are validated
// before they are written into a Dockerfile CMD so a repo cannot inject
// Dockerfile instructions.
type startConfig struct {
	Port       int
	PortSource string
	Start      []string
	From       string
	Health     string
	Runtime    string
	Output     string
}

type forgeJSON struct {
	Port    int      `json:"port"`
	Start   []string `json:"start"`
	Health  string   `json:"health"`
	Runtime string   `json:"runtime"`
	Output  string   `json:"output"`
}

func loadStartConfig(idx fileIndex) (startConfig, error) {
	if idx.has("forge.json") {
		raw, err := idx.read("forge.json")
		if err != nil {
			return startConfig{}, fmt.Errorf("detect: read forge.json: %w", err)
		}
		var doc forgeJSON
		if err := json.Unmarshal(raw, &doc); err != nil {
			return startConfig{}, fmt.Errorf("detect: forge.json is not valid JSON")
		}
		if len(doc.Start) > 0 {
			if err := validateArgv(doc.Start); err != nil {
				return startConfig{}, fmt.Errorf("detect: forge.json start: %w", err)
			}
		}
		cfg := startConfig{Start: doc.Start, From: "forge.json"}
		if doc.Port >= 1 && doc.Port <= 65535 {
			cfg.Port = doc.Port
			cfg.PortSource = "forge_json"
		}
		if h, err := normalizeForgeHealth(doc.Health); err != nil {
			return startConfig{}, err
		} else {
			cfg.Health = h
		}
		if r, err := normalizeForgeRuntime(doc.Runtime); err != nil {
			return startConfig{}, err
		} else {
			cfg.Runtime = r
		}
		if o, err := normalizeForgeOutput(doc.Output); err != nil {
			return startConfig{}, err
		} else {
			cfg.Output = o
		}
		return cfg, nil
	}
	if idx.has("Procfile") {
		raw, err := idx.read("Procfile")
		if err != nil {
			return startConfig{}, err
		}
		argv, err := parseProcfileWeb(raw)
		if err != nil {
			return startConfig{}, err
		}
		if len(argv) > 0 {
			return startConfig{Start: argv, From: "Procfile"}, nil
		}
	}
	return startConfig{}, nil
}

func parseProcfileWeb(raw []byte) ([]string, error) {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "web" {
			continue
		}
		argv := strings.Fields(rest)
		if len(argv) == 0 {
			return nil, fmt.Errorf("detect: Procfile web process is empty")
		}
		if err := validateArgv(argv); err != nil {
			return nil, fmt.Errorf("detect: Procfile web: %w", err)
		}
		return argv, nil
	}
	return nil, nil
}

func validateArgv(argv []string) error {
	if len(argv) == 0 || len(argv) > 16 {
		return fmt.Errorf("start must have 1–16 arguments")
	}
	for _, a := range argv {
		if a == "" || len(a) > 128 {
			return fmt.Errorf("invalid start argument")
		}
		for _, r := range a {
			if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_./:=+-@,", r)) {
				return fmt.Errorf("start argument %q contains unsupported characters", a)
			}
		}
	}
	return nil
}

func dockerfileCMD(argv []string) string {
	raw, err := json.Marshal(argv)
	if err != nil {
		return "CMD [\"true\"]\n"
	}
	return "CMD " + string(raw) + "\n"
}

func needRunConfig(language, why string) error {
	return fmt.Errorf("Unable to determine a safe %s start command. %s\n\nAdd a Dockerfile, or a forge.json with a \"start\" argv array, or a Procfile web process.\n\nExample forge.json:\n{\n  \"port\": 8080,\n  \"start\": [\"gunicorn\", \"mysite.wsgi:application\", \"--bind\", \"0.0.0.0:8080\"]\n}", language, why)
}

func applyStartPort(res Result, cfg startConfig) Result {
	if cfg.Port >= 1 && cfg.Port <= 65535 {
		res = withPort(res, cfg.Port, cfg.PortSource)
	}
	if cfg.Health != "" {
		res.HealthPath = cfg.Health
	}
	if cfg.Runtime != "" {
		res.Runtime = cfg.Runtime
	}
	if cfg.Output != "" {
		res.OutputDir = cfg.Output
	}
	return res
}

func normalizeForgeHealth(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if !strings.HasPrefix(s, "/") || strings.Contains(s, "://") || strings.ContainsAny(s, "\r\n\x00 ") || len(s) > 200 {
		return "", fmt.Errorf("detect: forge.json health must be a path starting with /")
	}
	return s, nil
}

func normalizeForgeRuntime(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", nil
	}
	if s != RuntimeServer && s != RuntimeStatic {
		return "", fmt.Errorf("detect: forge.json runtime must be %q or %q", RuntimeServer, RuntimeStatic)
	}
	return s, nil
}

func normalizeForgeOutput(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if strings.Contains(s, "..") || strings.ContainsAny(s, `/\`) || !safeIdent.MatchString(s) {
		return "", fmt.Errorf("detect: forge.json output must be a single directory name (dist, build, out)")
	}
	return s, nil
}
