package detect

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// exposeLine is `EXPOSE 3000` or `EXPOSE 3000/tcp`. First numeric token wins.
var exposeLine = regexp.MustCompile(`(?i)^\s*EXPOSE\s+(\d{1,5})(?:/\w+)?`)

// parseDockerfileExpose returns the first EXPOSE port in a Dockerfile.
//
// ARG/ENV substitution is not expanded: that would require a mini
// parser and could hide an undetermined port. If there is no literal
// EXPOSE, ok is false — the caller records forge_default instead of
// inventing a number from the rest of the file.
func parseDockerfileExpose(body []byte) (port int, ok bool) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := exposeLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > 65535 {
			continue
		}
		return n, true
	}
	return 0, false
}

func dockerfileLooksValid(body []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(line), "FROM ") {
			return true
		}
	}
	return false
}

func withPort(r Result, port int, source string) Result {
	if port < 1 || port > 65535 {
		port = DefaultPort
		source = "forge_default"
	}
	r.Port = port
	r.PortSource = source
	return r
}
