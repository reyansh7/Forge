package detect

import "strings"

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
	res = applyStartPort(res, cfg)
	if len(cfg.Start) > 0 {
		res.GeneratedDockerfile = goImage + dockerfileCMD(cfg.Start)
	} else {
		res.GeneratedDockerfile = goDockerfile
	}
	return res, nil
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

// Same recipe as Phase 0 so existing Go-without-Dockerfile deploys
// keep the same image contract (PORT=8080, binary /app/app).
const goDockerfile = `FROM golang:1.22-alpine
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -o /app/app .
ENV PORT=8080
EXPOSE 8080
CMD ["/app/app"]
`
