package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Diagnosis is one read-only finding from the deployment doctor.
//
// Evidence names files and environment keys. It must not contain a
// value. Confidence is "confirmed" when Forge compared the key with
// stored environment, and "hypothesis" when this is only a repository
// plan and the stored values are unknown. Risk "approval" means Forge
// will not write the value. Blocking findings fail the deploy. An
// environment gap is not blocking: a missing sample key is not proof
// the process requires it, and Forge does not invent a credential.
type Diagnosis struct {
	Code           string `json:"code"`
	Stage          string `json:"stage"`
	Evidence       string `json:"evidence"`
	Confidence     string `json:"confidence"`
	Recommendation string `json:"recommendation"`
	Risk           string `json:"risk"`
	Blocking       bool   `json:"blocking"`
}

// CompareEnv reads key names from an example file and compares them
// with configured. known is false for a repository plan that has not
// loaded the application's environment. The value side of each line is
// discarded and is not returned. A symlink is not opened.
func CompareEnv(dir string, configured []string, known bool) []Diagnosis {
	file, keys := exampleEnvKeys(dir)
	if file == "" || len(keys) == 0 {
		return nil
	}
	have := map[string]struct{}{}
	for _, key := range configured {
		have[key] = struct{}{}
	}
	var out []Diagnosis
	for _, key := range keys {
		evidence := file + " key " + key
		if reservedEnvKey(key) {
			out = append(out, Diagnosis{
				Code:           "env_reserved",
				Stage:          "detecting",
				Evidence:       evidence,
				Confidence:     "confirmed",
				Recommendation: "Leave this key unset in Forge. PORT is assigned by the platform, and FORGE_ keys are reserved.",
				Risk:           "none",
			})
			continue
		}
		if !known {
			out = append(out, Diagnosis{
				Code:           "env_declared",
				Stage:          "detecting",
				Evidence:       evidence,
				Confidence:     "hypothesis",
				Recommendation: "The example file names this key. Compare it with the Environment tab. The example value was discarded and will not be copied.",
				Risk:           "approval",
			})
			continue
		}
		if _, ok := have[key]; ok {
			continue
		}
		rec := "Set this key in the Environment tab if the process needs it. Forge will not invent a value."
		if isPublicEnvKey(key) {
			rec = "This is a build-time key. Set it before the next deploy if the browser bundle needs it. An empty value is what the build will inline. Forge will not invent one."
		}
		out = append(out, Diagnosis{
			Code:           "env_missing",
			Stage:          "detecting",
			Evidence:       evidence,
			Confidence:     "confirmed",
			Recommendation: rec,
			Risk:           "approval",
		})
	}
	return out
}

// FormatDoctor is the log line for a deploy. It repeats codes and key
// names only.
func FormatDoctor(list []Diagnosis) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[doctor] environment\n")
	for _, d := range list {
		fmt.Fprintf(&b, "%s %s confidence=%s\n", d.Code, d.Evidence, d.Confidence)
	}
	return b.String()
}

func exampleEnvKeys(dir string) (string, []string) {
	for _, name := range []string{".env.example", ".env.sample", ".env.template"} {
		path := filepath.Join(dir, name)
		st, err := os.Lstat(path)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 64<<10 {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return name, envKeysOnly(string(body))
	}
	return "", nil
}

func envKeysOnly(content string) []string {
	seen := map[string]struct{}{}
	var keys []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if !doctorKeyOK(key) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func doctorKeyOK(key string) bool {
	if key == "" || len(key) > 128 {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func reservedEnvKey(key string) bool {
	return key == "PORT" || strings.HasPrefix(key, "FORGE_")
}
