package detect

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RejectInvalidExpose fails closed when a Dockerfile names an EXPOSE
// port outside 1–65535. A missing EXPOSE is not an error: packs use
// the Forge default. The file is not modified. The line is reported
// without any surrounding environment values.
func RejectInvalidExpose(dir string) error {
	body, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		return nil
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(line), "EXPOSE ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return fmt.Errorf("invalid_port: Dockerfile EXPOSE has no port")
		}
		raw := fields[1]
		raw = strings.Split(raw, "/")[0]
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid_port: Dockerfile EXPOSE %s is not a port from 1 to 65535", raw)
		}
		return nil
	}
	return nil
}

// preflightDetail prefixes a detector error with a stable code. The
// message is the detector's own text. Environment file values are not
// read and are not added here.
func preflightDetail(rel string, err error) string {
	msg := err.Error()
	code := "detect_failed"
	switch {
	case strings.Contains(msg, "start command"):
		code = "missing_start"
	case strings.Contains(msg, "docker-compose") || strings.Contains(msg, "Compose"):
		code = "unsupported_compose"
	case strings.Contains(msg, "deployment strategy") || strings.Contains(msg, "No supported build strategy"):
		code = "unsupported_strategy"
	}
	if rel != "" && rel != "." {
		return code + " (" + rel + "): " + msg
	}
	return code + ": " + msg
}
