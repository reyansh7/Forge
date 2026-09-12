package detect

import "strings"

type rubyPack struct{}

func (rubyPack) ID() Kind { return KindRuby }

func (rubyPack) Match(idx fileIndex) bool { return idx.has("Gemfile") }

func (rubyPack) Analyze(idx fileIndex) (Result, error) {
	res := withPort(baseResult(KindRuby, "ruby", "Gemfile", "Bundler project"), DefaultPort, "pack")
	if raw, err := idx.read("Gemfile"); err == nil {
		if strings.Contains(string(raw), "rails") {
			res.Framework = "rails"
		}
	}
	cfg, err := loadStartConfig(idx)
	if err != nil {
		return Result{}, err
	}
	res = applyStartPort(res, cfg)

	var cmd []string
	switch {
	case len(cfg.Start) > 0:
		cmd = cfg.Start
	case idx.hasRel("config/puma.rb"):
		cmd = []string{"bundle", "exec", "puma", "-C", "config/puma.rb"}
	default:
		return Result{}, needRunConfig("Ruby", "No config/puma.rb, Procfile, or forge.json start. rails server and rackup are not assumed.")
	}
	res.GeneratedDockerfile = rubyImage() + dockerfileCMD(cmd)
	return res, nil
}

func rubyImage() string {
	return `FROM ruby:3.3
WORKDIR /app
COPY . .
RUN bundle install
ENV PORT=8080
EXPOSE 8080
`
}
