package detect

import (
	"encoding/json"
	"strconv"
	"strings"
)

type nodePack struct{}

func (nodePack) ID() Kind { return KindNode }

func (nodePack) Match(idx fileIndex) bool { return idx.has("package.json") }

type npmManifest struct {
	Name                 string            `json:"name"`
	Main                 string            `json:"main"`
	Scripts              map[string]string `json:"scripts"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

func (nodePack) Analyze(idx fileIndex) (Result, error) {
	res := withPort(baseResult(KindNode, "javascript", "package.json", "Node.js manifest"), DefaultPort, "pack")
	raw, err := idx.read("package.json")
	if err != nil {
		return Result{}, err
	}
	var man npmManifest
	if json.Unmarshal(raw, &man) != nil {
		// Malformed JSON: still Node (the file exists) but generic
		// recipe. Do not invent a framework from broken text.
		res.GeneratedDockerfile = nodeGenericDockerfile
		return res, nil
	}
	res.Framework = nodeFramework(man)
	cfg, err := loadStartConfig(idx)
	if err != nil {
		return Result{}, err
	}
	res = applyListenScan(res, scanListen(idx))
	res = applyStartPort(res, cfg)
	// forge.json health wins. Otherwise use a GET route the entry
	// file actually mounts so Express-style APIs are not FAILED on
	// GET / → 404 (which also deletes the just-started container).
	if strings.TrimSpace(res.HealthPath) == "" {
		if paths := scanGETPaths(idx); len(paths) > 0 {
			res.HealthPath = paths[0]
		}
	}
	df, extra, err := nodeDockerfile(idx, man, res, cfg)
	if err != nil {
		return Result{}, err
	}
	res.GeneratedDockerfile = df
	res.ExtraFiles = extra
	if extra[nginxForgeName] != "" {
		res.Runtime = RuntimeStatic
		if res.OutputDir == "" {
			res.OutputDir = staticOutputDir(res.Framework, idx)
		}
	}
	return res, nil
}

func nodeFramework(man npmManifest) string {
	has := func(name string) bool {
		_, a := man.Dependencies[name]
		_, b := man.DevDependencies[name]
		_, c := man.OptionalDependencies[name]
		return a || b || c
	}
	switch {
	case has("next"):
		return "next"
	case has("nuxt") || has("nuxt3"):
		return "nuxt"
	case has("@nestjs/core"):
		return "nest"
	case has("fastify"):
		return "fastify"
	case has("express"):
		return "express"
	case has("vite") && (has("react") || has("react-dom")):
		return "vite-react"
	case has("vite") && has("vue"):
		return "vite-vue"
	case has("vite") && (has("svelte") || has("@sveltejs/vite-plugin-svelte")):
		return "vite-svelte"
	case has("vite"):
		return "vite"
	case has("react-scripts"):
		return "cra"
	case has("react") || has("react-dom"):
		return "react"
	case has("vue"):
		return "vue"
	case has("svelte"):
		return "svelte"
	default:
		return ""
	}
}

func nodeDockerfile(idx fileIndex, man npmManifest, res Result, cfg startConfig) (string, map[string]string, error) {
	install, run := "npm install", "npm"
	switch {
	case idx.has("pnpm-lock.yaml"):
		install = "npm install -g pnpm && pnpm install"
		run = "pnpm"
	case idx.has("yarn.lock"):
		install = "yarn install --frozen-lockfile"
		run = "yarn"
	case idx.has("package-lock.json"):
		install = "npm ci"
		run = "npm"
	}

	startScript := strings.TrimSpace(man.Scripts["start"])
	hasBuild := strings.TrimSpace(man.Scripts["build"]) != ""
	prodStart := startScript != "" && !isDevStartScript(startScript)

	// Explicit forge.json runtime wins. Otherwise a frontend toolchain
	// with a build script and no production start is a static site:
	// `vite build` writes dist/; it does not start HTTP.
	static := cfg.Runtime == RuntimeStatic ||
		(cfg.Runtime != RuntimeServer && isStaticFrontend(res.Framework, man) && hasBuild && !prodStart && len(cfg.Start) == 0)

	if static {
		outDir := res.OutputDir
		if outDir == "" {
			outDir = staticOutputDir(res.Framework, idx)
		}
		res.Runtime = RuntimeStatic
		res.OutputDir = outDir
		return nodeStaticDockerfile(install, run, hasBuild, outDir, res.ListenPort(), res.Framework), map[string]string{
			nginxForgeName: nginxSPAConf(res.ListenPort()),
		}, nil
	}

	// Keep the Phase 0 recipe when package.json only declares npm start.
	if res.Framework == "" && !hasBuild && len(cfg.Start) == 0 && startScript == "" {
		return nodeGenericDockerfile, nil, nil
	}

	var b strings.Builder
	b.WriteString("FROM node:20-alpine\nWORKDIR /app\nCOPY . .\n")
	b.WriteString("RUN ")
	b.WriteString(install)
	b.WriteString("\n")
	if hasBuild {
		b.WriteString("RUN ")
		b.WriteString(run)
		if run == "npm" {
			b.WriteString(" run build\n")
		} else {
			b.WriteString(" build\n")
		}
	}
	port := res.ListenPort()
	b.WriteString("ENV PORT=")
	b.WriteString(strconv.Itoa(port))
	b.WriteString("\nENV HOST=0.0.0.0\nEXPOSE ")
	b.WriteString(strconv.Itoa(port))
	b.WriteString("\n")

	switch {
	case len(cfg.Start) > 0:
		b.WriteString(dockerfileCMD(cfg.Start))
	case prodStart:
		if run == "npm" {
			b.WriteString(`CMD ["npm","start"]` + "\n")
		} else if run == "yarn" {
			b.WriteString(`CMD ["yarn","start"]` + "\n")
		} else {
			b.WriteString(`CMD ["pnpm","start"]` + "\n")
		}
	case res.Framework == "next":
		// next start is Next.js production after `next build`, not next dev.
		b.WriteString(`CMD ["npx","next","start","-H","0.0.0.0","-p","`)
		b.WriteString(strconv.Itoa(port))
		b.WriteString(`"]` + "\n")
	case res.Framework == "nest" && hasBuild:
		b.WriteString(`CMD ["node","dist/main.js"]` + "\n")
	default:
		return "", nil, nodeNeedStart(man, res.Framework)
	}
	return b.String(), nil, nil
}

func isViteFramework(framework string) bool {
	switch framework {
	case "vite", "vite-react", "vite-vue", "vite-svelte":
		return true
	default:
		return false
	}
}

func isStaticFrontend(framework string, man npmManifest) bool {
	switch framework {
	case "vite", "vite-react", "vite-vue", "vite-svelte", "cra":
		return true
	case "react", "vue", "svelte":
		// A React library that also depends on Express is a server.
		return man.Scripts["build"] != "" && man.Dependencies["express"] == "" && man.Dependencies["fastify"] == ""
	default:
		return false
	}
}

func isDevStartScript(script string) bool {
	s := strings.ToLower(script)
	switch {
	case strings.Contains(s, "next dev"), strings.Contains(s, "nuxt dev"):
		return true
	case strings.Contains(s, "react-scripts start"):
		return true
	case strings.Contains(s, "webpack-dev-server"):
		return true
	case strings.Contains(s, "vite preview"), strings.Contains(s, "vite --"):
		return true
	case s == "vite" || strings.HasPrefix(s, "vite "):
		return true
	default:
		return false
	}
}

func staticOutputDir(framework string, idx fileIndex) string {
	if framework == "cra" {
		return "build"
	}
	if raw, err := readEntry(idx, "vite.config.js"); err == nil {
		if d := viteOutDir(string(raw)); d != "" {
			return d
		}
	}
	if raw, err := readEntry(idx, "vite.config.ts"); err == nil {
		if d := viteOutDir(string(raw)); d != "" {
			return d
		}
	}
	return "dist"
}

func viteOutDir(body string) string {
	// outDir: 'build' — only a single identifier, never interpolated.
	const key = "outDir"
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	rest := body[i+len(key):]
	rest = strings.TrimLeft(rest, " \t\n:")
	if len(rest) < 3 {
		return ""
	}
	q := rest[0]
	if q != '\'' && q != '"' {
		return ""
	}
	end := strings.IndexByte(rest[1:], q)
	if end < 1 {
		return ""
	}
	dir := rest[1 : 1+end]
	if strings.Contains(dir, "..") || strings.ContainsAny(dir, `/\`) || !safeIdent.MatchString(dir) {
		return ""
	}
	return dir
}

func nodeStaticDockerfile(install, run string, hasBuild bool, outDir string, port int, framework string) string {
	if outDir == "" {
		outDir = "dist"
	}
	var b strings.Builder
	b.WriteString("FROM node:20-alpine AS build\nWORKDIR /app\nCOPY . .\nRUN ")
	b.WriteString(install)
	b.WriteString("\n")
	if hasBuild {
		// Vite/CRA default base is `/`. Caddy's public path URL is
		// /d/{deployment_id}/, so a built `<script src="/assets/x.js">`
		// is fetched from the proxy origin root and 404s. A relative
		// base keeps assets under the deployment prefix and still
		// works on {slug}.localhost (mounted at /). We pass flags to
		// the existing build script; the repository is not edited.
		b.WriteString("RUN ")
		switch {
		case framework == "cra":
			b.WriteString("PUBLIC_URL=. ")
			b.WriteString(run)
			if run == "npm" {
				b.WriteString(" run build\n")
			} else {
				b.WriteString(" build\n")
			}
		case isViteFramework(framework) && run == "npm":
			b.WriteString("npm run build -- --base ./\n")
		case isViteFramework(framework) && run == "yarn":
			b.WriteString("yarn build --base ./\n")
		case isViteFramework(framework) && run == "pnpm":
			b.WriteString("pnpm build --base ./\n")
		default:
			b.WriteString(run)
			if run == "npm" {
				b.WriteString(" run build\n")
			} else {
				b.WriteString(" build\n")
			}
		}
	}
	b.WriteString("FROM nginx:1.27-alpine\n")
	b.WriteString("COPY ")
	b.WriteString(nginxForgeName)
	b.WriteString(" /etc/nginx/nginx.conf\n")
	b.WriteString("COPY --from=build /app/")
	b.WriteString(outDir)
	b.WriteString(" /usr/share/nginx/html\n")
	b.WriteString("RUN mkdir -p /var/lib/nginx-tmp && chown -R nginx:nginx /var/lib/nginx-tmp\n")
	b.WriteString("USER nginx\nEXPOSE ")
	b.WriteString(strconv.Itoa(port))
	b.WriteString("\nENTRYPOINT [\"nginx\"]\nCMD [\"-g\",\"daemon off;\"]\n")
	return b.String()
}

func nodeNeedStart(man npmManifest, framework string) error {
	var scripts []string
	for _, name := range []string{"dev", "build", "lint", "preview", "start", "test"} {
		if man.Scripts[name] != "" {
			scripts = append(scripts, name)
		}
	}
	why := "No package.json start script, Procfile, or forge.json start. Dev servers (next dev, vite) are not used."
	if framework != "" {
		why = "Detected " + framework + ". " + why
	}
	if len(scripts) > 0 {
		why += " Detected scripts: " + strings.Join(scripts, ", ") + "."
	}
	if isStaticFrontend(framework, man) {
		why += " A frontend with a build script is served as a static site; this repository has no build script."
	}
	return needRunConfig("Node.js", why)
}

const nodeGenericDockerfile = `FROM node:20-alpine
WORKDIR /app
COPY . .
RUN npm install --omit=dev
ENV PORT=8080
ENV HOST=0.0.0.0
EXPOSE 8080
CMD ["npm","start"]
`
