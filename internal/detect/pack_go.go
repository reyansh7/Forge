package detect

import (
	"os"
	"path/filepath"
	"strings"
)

type goPack struct{}

func (goPack) ID() Kind { return KindGo }

func (goPack) Match(idx fileIndex) bool { return idx.has("go.mod") }

func (p goPack) Analyze(idx fileIndex) (Result, error) {
	res := withPort(baseResult(KindGo, "go", "go.mod", "Go module"), DefaultPort, "pack")
	if body, err := idx.read("go.mod"); err == nil {
		res.Framework = goFramework(string(body))
	}
	cfg, err := loadStartConfig(idx)
	if err != nil {
		return Result{}, err
	}
	// An operator (or Deploy All) pins one program when the module has
	// several mains. The file is written by the worker from
	// applications.build_package. It is not a shell script: only "."
	// and ./cmd/<name> are accepted. A user Dockerfile still wins,
	// because that pack runs before goPack.
	if target, ok := forcedGoTarget(idx); ok {
		res.GeneratedDockerfile = goDockerfileFor(target)
		return res, nil
	}
	res = applyStartPort(res, cfg)
	if len(cfg.Start) > 0 {
		res.GeneratedDockerfile = goImage + dockerfileCMD(cfg.Start)
		return res, nil
	}
	pkgs := goMainPackages(idx)
	switch len(pkgs) {
	case 1:
		res.GeneratedDockerfile = goDockerfileFor(pkgs[0])
		return res, nil
	case 0:
		return Result{}, needRunConfig("Go", "go.mod is present but there is no package main in the root or in cmd/<name>.")
	default:
		return Result{}, needRunConfig("Go", "Several main packages: "+strings.Join(pkgs, ", ")+". Deploy one service at a time, or add forge.json \"start\".")
	}
}

func forcedGoTarget(idx fileIndex) (string, bool) {
	raw, err := idx.read(".forge-build-package")
	if err != nil {
		return "", false
	}
	target := strings.TrimSpace(string(raw))
	if target == "." {
		return ".", true
	}
	name, ok := strings.CutPrefix(target, "./cmd/")
	if !ok || strings.Contains(name, "/") || !goCmdNameOK(name) {
		return "", false
	}
	return "./cmd/" + name, true
}

func goCmdNameOK(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func directoryHasMain(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			continue
		}
		if goFileIsMain(string(body)) {
			return true
		}
	}
	return false
}

func goFileIsMain(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "package ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "package ")) == "main"
		}
	}
	return false
}

func goDockerfileFor(target string) string {
	return `FROM golang:1.22-alpine
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -o /app/app ` + target + `
ENV PORT=8080
EXPOSE 8080
CMD ["/app/app"]
`
}

const goImage = `FROM golang:1.22-alpine
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -o /app/app .
ENV PORT=8080
EXPOSE 8080
`

func goFramework(mod string) string {
	switch {
	case strings.Contains(mod, "github.com/gin-gonic/gin"):
		return "gin"
	case strings.Contains(mod, "github.com/labstack/echo"):
		return "echo"
	case strings.Contains(mod, "github.com/gofiber/fiber"):
		return "fiber"
	default:
		return ""
	}
}
