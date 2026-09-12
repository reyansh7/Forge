package detect

import (
	"os"
	"strings"
)

type pythonPack struct{}

func (pythonPack) ID() Kind { return KindPython }

func (pythonPack) Match(idx fileIndex) bool {
	return idx.hasAny("requirements.txt", "pyproject.toml", "Pipfile", "setup.py")
}

func (pythonPack) Analyze(idx fileIndex) (Result, error) {
	manifest := "requirements.txt"
	switch {
	case idx.has("pyproject.toml"):
		manifest = "pyproject.toml"
	case idx.has("Pipfile"):
		manifest = "Pipfile"
	case idx.has("setup.py"):
		manifest = "setup.py"
	}
	res := withPort(baseResult(KindPython, "python", manifest, "Python project"), DefaultPort, "pack")
	res.Framework = pythonFramework(idx)
	cfg, err := loadStartConfig(idx)
	if err != nil {
		return Result{}, err
	}
	res = applyStartPort(res, cfg)

	res = applyListenScan(res, scanListen(idx))
	var cmd []string
	switch {
	case len(cfg.Start) > 0:
		cmd = cfg.Start
	case res.Framework == "django":
		wsgi, ok := findUniqueWSGI(idx)
		if !ok {
			return Result{}, needRunConfig("Python/Django", "No single */wsgi.py, Procfile, or forge.json start. Django's runserver is not used.")
		}
		cmd = []string{"gunicorn", wsgi, "--bind", "0.0.0.0:8080"}
	case res.Framework == "fastapi":
		mod, ok := findUniqueASGI(idx)
		if !ok {
			return Result{}, needRunConfig("Python/FastAPI", "No single main.py/app.py declaring FastAPI(), and no Procfile or forge.json start.")
		}
		cmd = []string{"uvicorn", mod, "--host", "0.0.0.0", "--port", "8080"}
	case res.Framework == "flask":
		mod, ok := findUniqueFlask(idx)
		if !ok {
			return Result{}, needRunConfig("Python/Flask", "No single app.py declaring Flask(), and no Procfile or forge.json start. The Flask development server is not used.")
		}
		cmd = []string{"gunicorn", mod, "--bind", "0.0.0.0:8080"}
	default:
		return Result{}, needRunConfig("Python", "No Procfile or forge.json start. Entrypoint files are not guessed.")
	}

	res.GeneratedDockerfile = pythonImage(idx, res.Framework) + dockerfileCMD(cmd)
	return res, nil
}

func pythonFramework(idx fileIndex) string {
	blob := pythonDepText(idx)
	switch {
	case idx.has("manage.py") || strings.Contains(blob, "django"):
		return "django"
	case strings.Contains(blob, "fastapi"):
		return "fastapi"
	case strings.Contains(blob, "flask"):
		return "flask"
	default:
		return ""
	}
}

func pythonDepText(idx fileIndex) string {
	var b strings.Builder
	for _, name := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py"} {
		if raw, err := idx.read(name); err == nil {
			b.Write(raw)
			b.WriteByte('\n')
		}
	}
	return strings.ToLower(b.String())
}

func pythonImage(idx fileIndex, framework string) string {
	var install string
	switch {
	case idx.has("requirements.txt"):
		install = "RUN pip install --no-cache-dir -r requirements.txt\n"
	case idx.has("Pipfile"):
		install = "RUN pip install --no-cache-dir pipenv && pipenv install --system --deploy\n"
	default:
		install = "RUN pip install --no-cache-dir .\n"
	}
	deps := pythonDepText(idx)
	if (framework == "django" || framework == "flask") && !strings.Contains(deps, "gunicorn") {
		install += "RUN pip install --no-cache-dir gunicorn\n"
	}
	if framework == "fastapi" && !strings.Contains(deps, "uvicorn") {
		install += "RUN pip install --no-cache-dir uvicorn\n"
	}
	return "FROM python:3.12-slim\nWORKDIR /app\nCOPY . .\n" + install +
		"ENV PORT=8080\nEXPOSE 8080\n"
}

// findUniqueWSGI looks for exactly one <pkg>/wsgi.py. That file is
// Django's declared WSGI module, not a guessed main.py.
func findUniqueWSGI(idx fileIndex) (string, bool) {
	var found []string
	ents, err := os.ReadDir(idx.root)
	if err != nil {
		return "", false
	}
	for _, e := range ents {
		if !e.IsDir() {
			if e.Name() == "wsgi.py" {
				found = append(found, "wsgi:application")
			}
			continue
		}
		if _, skip := skipWalkDirs[e.Name()]; skip {
			continue
		}
		if !fileNameOK(e.Name()) {
			continue
		}
		rel := e.Name() + "/wsgi.py"
		if idx.hasRel(rel) {
			if id := ident(e.Name()); id != "" {
				found = append(found, id+".wsgi:application")
			}
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

func findUniqueASGI(idx fileIndex) (string, bool) {
	return findUniquePyApp(idx, "FastAPI", []string{"main.py", "app.py", "api.py"})
}

func findUniqueFlask(idx fileIndex) (string, bool) {
	return findUniquePyApp(idx, "Flask(", []string{"app.py", "wsgi.py", "main.py"})
}

func findUniquePyApp(idx fileIndex, marker string, names []string) (string, bool) {
	var found []string
	for _, name := range names {
		raw, err := idx.read(name)
		if err != nil {
			continue
		}
		if !strings.Contains(string(raw), marker) {
			continue
		}
		mod := strings.TrimSuffix(name, ".py")
		if ident(mod) == "" {
			continue
		}
		found = append(found, mod+":app")
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}
