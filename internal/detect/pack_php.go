package detect

import (
	"encoding/json"
	"strings"
)

type phpPack struct{}

func (phpPack) ID() Kind { return KindPHP }

func (phpPack) Match(idx fileIndex) bool { return idx.has("composer.json") }

func (phpPack) Analyze(idx fileIndex) (Result, error) {
	res := withPort(baseResult(KindPHP, "php", "composer.json", "Composer project"), DefaultPort, "pack")
	if raw, err := idx.read("composer.json"); err == nil {
		res.Framework = phpFramework(raw)
	}
	cfg, err := loadStartConfig(idx)
	if err != nil {
		return Result{}, err
	}
	res = applyStartPort(res, cfg)
	if len(cfg.Start) == 0 {
		return Result{}, needRunConfig("PHP", "php -S is a development server and is not used. Declare start in forge.json or a Procfile.")
	}
	res.GeneratedDockerfile = phpImage() + dockerfileCMD(cfg.Start)
	return res, nil
}

func phpFramework(raw []byte) string {
	var doc struct {
		Require    map[string]string `json:"require"`
		RequireDev map[string]string `json:"require-dev"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		if strings.Contains(strings.ToLower(string(raw)), "laravel") {
			return "laravel"
		}
		return ""
	}
	for _, m := range []map[string]string{doc.Require, doc.RequireDev} {
		if _, ok := m["laravel/framework"]; ok {
			return "laravel"
		}
	}
	return ""
}

func phpImage() string {
	return `FROM php:8.3-cli
COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
WORKDIR /app
COPY . .
RUN composer install --no-dev --no-interaction --ignore-platform-reqs || true
ENV PORT=8080
EXPOSE 8080
`
}
