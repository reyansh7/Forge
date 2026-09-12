package detect

import (
	"fmt"
	"regexp"
)

type rustPack struct{}

func (rustPack) ID() Kind { return KindRust }

func (rustPack) Match(idx fileIndex) bool { return idx.has("Cargo.toml") }

var cargoName = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)

func (rustPack) Analyze(idx fileIndex) (Result, error) {
	res := withPort(baseResult(KindRust, "rust", "Cargo.toml", "Cargo project"), DefaultPort, "pack")
	raw, err := idx.read("Cargo.toml")
	if err != nil {
		return Result{}, err
	}
	name := "app"
	if m := cargoName.FindSubmatch(raw); len(m) == 2 {
		if id := ident(string(m[1])); id != "" {
			name = id
		}
	}
	if ident(name) == "" {
		return Result{}, fmt.Errorf("detect: Cargo.toml package name is not a safe identifier; add a Dockerfile")
	}
	res.GeneratedDockerfile = rustDockerfile(name)
	return res, nil
}

func rustDockerfile(name string) string {
	// name is already safeIdent. Same-image build keeps the recipe
	// simple; the rust toolchain image is large but isolated.
	return `FROM rust:1-bookworm
WORKDIR /app
COPY . .
RUN cargo build --release
ENV PORT=8080
EXPOSE 8080
CMD ["./target/release/` + name + `"]
`
}
