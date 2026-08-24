package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadDotEnv reads a local .env file into the process environment.
//
// Why this exists: Compose uses `.env` for ${VAR} interpolation in
// docker-compose.yml. `go run ./cmd/api` is a separate process and does
// not see that file unless we load it.
//
// Already-set keys are left alone so CI, production, and explicit
// `set FORGE_*=` in the shell always win over a leftover .env.
// Values are never logged.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		// Missing .env is normal in CI or when the operator exported vars.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		key, val, ok, err := parseDotEnvLine(scanner.Text())
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		if !ok {
			continue // blank line or comment
		}
		// LookupEnv distinguishes "unset" from "set to empty string".
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	return scanner.Err()
}

// parseDotEnvLine understands a small KEY=VALUE dialect: comments, blanks,
// optional `export ` prefix, and simple quoted values. It is not a full
// bash parser — that keeps the surface small and avoids executing anything.
func parseDotEnvLine(line string) (key, val string, ok bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}
	eq := strings.IndexByte(line, '=')
	// eq <= 0 means no '=', or the key would be empty ("=value").
	if eq <= 0 {
		return "", "", false, fmt.Errorf("expected KEY=VALUE")
	}
	key = strings.TrimSpace(line[:eq])
	if key == "" {
		return "", "", false, fmt.Errorf("empty key")
	}
	val = strings.TrimSpace(line[eq+1:])
	val = unquote(val)
	return key, val, true, nil
}

// unquote strips one matching pair of " or ' around the value so
// FORGE_API_ADDR="127.0.0.1:8080" works like the unquoted form.
func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
