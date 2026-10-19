package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeProjectAdvice explains a production Compose file when Forge
// must not deploy the repository root as one application.
//
// docker-compose.dev.yml is ignored. Environment values are never
// copied into the message: a Compose file often contains default
// passwords. An empty string means there is no multi-service production
// Compose file to report.
func composeProjectAdvice(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil || len(raw) == 0 || len(raw) > 64<<10 {
		return ""
	}
	var file struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil || len(file.Services) < 2 {
		return ""
	}
	names := make([]string, 0, len(file.Services))
	for name := range file.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("This repository is a multi-service project. docker-compose.yml lists ")
	b.WriteString(strings.Join(names, ", "))
	b.WriteString(". Forge did not deploy the repository root as one application. docker-compose.dev.yml is ignored.\n\n")
	var buildLines []string
	var imageLines []string
	for _, name := range names {
		svc := file.Services[name]
		if image, _ := svc["image"].(string); strings.TrimSpace(image) != "" && svc["build"] == nil {
			imageLines = append(imageLines, fmt.Sprintf("- %s uses image %s. Forge does not pull or run prebuilt images. It is not given a public hostname. A named volume declared for it is not created or deleted by this deploy, rollback, stop, or cleanup.", name, strings.TrimSpace(image)))
			continue
		}
		root, docker := composeBuild(svc["build"])
		if root == "" {
			root = "."
		}
		if !composeRootSafe(root) {
			imageLines = append(imageLines, fmt.Sprintf("- %s build context %q escapes the repository and was rejected.", name, root))
			continue
		}
		buildLines = append(buildLines, fmt.Sprintf("- %s builds from %s (Dockerfile %s).", name, root, docker))
	}
	if len(buildLines) > 0 {
		b.WriteString("Application services:\n")
		b.WriteString(strings.Join(buildLines, "\n"))
		b.WriteString("\n\n")
	}
	if len(imageLines) > 0 {
		b.WriteString("Infrastructure services Forge cannot represent:\n")
		b.WriteString(strings.Join(imageLines, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString("Add a forge.yaml at the repository root that names only the services Forge should build. Set public: true or false on each. A database image, a host port, a bind mount such as ./db/init.sql, and a Compose healthcheck command are not manifest fields. Do not copy passwords from the Compose file into forge.yaml or into NEXT_PUBLIC_* variables.\n\n")
	b.WriteString("Example for a frontend that already proxies /api to the bridge name api, with the API private:\n\n")
	b.WriteString("services:\n")
	b.WriteString("  - name: api\n")
	b.WriteString("    root: server\n")
	b.WriteString("    public: false\n")
	b.WriteString("    port: 5000\n")
	b.WriteString("    health: /api/health\n")
	b.WriteString("  - name: client\n")
	b.WriteString("    root: client\n")
	b.WriteString("    public: true\n")
	b.WriteString("    port: 80\n")
	b.WriteString("    health: /nginx-health\n")
	b.WriteString("    depends_on: [api]\n")
	return b.String()
}

func composeBuild(raw any) (root, dockerfile string) {
	switch v := raw.(type) {
	case string:
		return strings.Trim(strings.TrimSpace(v), "/"), "Dockerfile"
	case map[string]any:
		ctx, _ := v["context"].(string)
		docker, _ := v["dockerfile"].(string)
		docker = strings.TrimSpace(docker)
		if docker == "" {
			docker = "Dockerfile"
		}
		return strings.Trim(strings.TrimSpace(ctx), "/"), docker
	default:
		return "", "Dockerfile"
	}
}

func composeRootSafe(root string) bool {
	if root == "" || root == "." {
		return true
	}
	if filepath.IsAbs(root) || strings.Contains(root, "\\") || strings.Contains(root, "..") {
		return false
	}
	return true
}

func manifestDeployHint(services []Service) string {
	var b strings.Builder
	b.WriteString("forge.yaml describes a multi-service project. Deploy it from the project, not as this one root application.\n")
	for _, svc := range services {
		pub := "private"
		if svc.Public != nil && *svc.Public {
			pub = "public"
		}
		fmt.Fprintf(&b, "- %s root %s port %d health %s %s\n", svc.Name, svc.Root, svc.Port, svc.Health, pub)
	}
	return b.String()
}
