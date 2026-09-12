package detect

import "fmt"

type dockerfilePack struct{}

func (dockerfilePack) ID() Kind { return KindDockerfile }

func (dockerfilePack) Match(idx fileIndex) bool {
	return idx.has("Dockerfile")
}

func (dockerfilePack) Analyze(idx fileIndex) (Result, error) {
	body, err := idx.read("Dockerfile")
	if err != nil {
		return Result{}, fmt.Errorf("detect: read Dockerfile: %w", err)
	}
	if !dockerfileLooksValid(body) {
		return Result{}, fmt.Errorf("detect: Dockerfile exists but is not a valid image recipe (missing FROM).\n\nAdd a FROM instruction or remove the file so native detection can run.")
	}
	res := baseResult(KindDockerfile, "any", "Dockerfile", "user Dockerfile is the universal strategy")
	res.Runtime = RuntimeImage
	if port, ok := parseDockerfileExpose(body); ok {
		return withPort(res, port, "dockerfile_expose"), nil
	}
	return withPort(res, DefaultPort, "forge_default"), nil
}
