package store

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// EnvIssue is a parse note the operator can read. Text names a line
// number and a key when the key is a valid identifier. It must not
// contain a value from the pasted file.
type EnvIssue struct {
	Line int
	Text string
}

// ParseOperatorEnv reads a pasted .env for one service.
//
// Comments and blank lines are ignored. Quoted values may contain "=".
// Duplicate keys keep the last line. Reserved keys (PORT, HOST,
// FORGE_*) are reported and omitted. A malformed line is reported
// without its text, so a secret on that line cannot reach the error.
// The returned values are for persistence only; callers must not log them.
func ParseOperatorEnv(text string) ([]EnvVar, []EnvIssue, error) {
	if len(text) > 64<<10 {
		return nil, nil, fmt.Errorf("environment text is too large")
	}
	text = strings.TrimPrefix(text, "\uFEFF")
	byKey := map[string]EnvVar{}
	order := make([]string, 0)
	var issues []EnvIssue
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, original := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(original)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			issues = append(issues, EnvIssue{Line: lineNo, Text: fmt.Sprintf("line %d is not KEY=VALUE", lineNo)})
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if err := ValidateEnvKey(key); err != nil {
			if strings.EqualFold(key, "PORT") || strings.EqualFold(key, "HOST") || strings.HasPrefix(strings.ToUpper(key), "FORGE_") {
				issues = append(issues, EnvIssue{Line: lineNo, Text: fmt.Sprintf("line %d: %s", lineNo, err.Error())})
			} else {
				issues = append(issues, EnvIssue{Line: lineNo, Text: fmt.Sprintf("line %d key is not a shell identifier", lineNo)})
			}
			continue
		}
		value, ok := unquoteOperatorValue(line[eq+1:])
		if !ok || strings.ContainsAny(value, "\r\n") || utf8.RuneCountInString(value) > maxEnvValueLen {
			issues = append(issues, EnvIssue{Line: lineNo, Text: fmt.Sprintf("line %d value is not a single line", lineNo)})
			continue
		}
		if _, exists := byKey[key]; exists {
			issues = append(issues, EnvIssue{Line: lineNo, Text: fmt.Sprintf("%s was set more than once; the last value is kept", key)})
		} else {
			order = append(order, key)
		}
		byKey[key] = EnvVar{Key: key, Value: value}
	}
	if len(byKey) > maxEnvVars {
		return nil, issues, fmt.Errorf("at most %d environment variables per application", maxEnvVars)
	}
	out := make([]EnvVar, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	return out, issues, nil
}

// EnvVarClass reports whether a key is inlined at image build.
// NEXT_PUBLIC_, VITE_, and REACT_APP_ are build-time. Every other key
// is injected when the container starts.
func EnvVarClass(key string) string {
	if strings.HasPrefix(key, "NEXT_PUBLIC_") || strings.HasPrefix(key, "VITE_") || strings.HasPrefix(key, "REACT_APP_") {
		return "build"
	}
	return "runtime"
}

// PlaceholderWarning names a key whose value looks like a sample
// credential. The value is not included in the result.
func PlaceholderWarning(key, value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return ""
	}
	if _, ok := obviousPlaceholders[v]; ok || strings.Contains(v, "changeme") || strings.Contains(v, "placeholder") || strings.Contains(v, "your-password") || strings.Contains(v, "your_password") || strings.Contains(v, "replace-me") || strings.Contains(v, "replace_me") {
		return key + " looks like a placeholder"
	}
	return ""
}

var obviousPlaceholders = map[string]struct{}{
	"password": {}, "secret": {}, "changeme": {}, "change-me": {}, "change_me": {},
	"your_password": {}, "your-password": {}, "placeholder": {}, "todo": {},
	"example": {}, "xxx": {}, "xxxx": {}, "admin": {}, "root": {}, "test": {},
	"123456": {}, "password123": {}, "postgres": {}, "postgres_secure_dev_password": {},
}

func unquoteOperatorValue(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1], true
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		inner := v[1 : len(v)-1]
		var b strings.Builder
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) {
				switch inner[i+1] {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(inner[i+1])
				}
				i++
				continue
			}
			b.WriteByte(inner[i])
		}
		return b.String(), true
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v, true
}
