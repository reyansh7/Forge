package detect

import (
	"regexp"
)

type ccPack struct{}

func (ccPack) ID() Kind { return KindCC }

func (ccPack) Match(idx fileIndex) bool {
	build := idx.has("CMakeLists.txt") || idx.has("Makefile") || idx.has("makefile")
	src := idx.hasSourceExt(".c", ".cc", ".cpp", ".cxx", ".h", ".hpp")
	return build && src
}

var cmakeExec = regexp.MustCompile(`(?i)add_executable\s*\(\s*([A-Za-z][A-Za-z0-9_.-]*)`)

func (ccPack) Analyze(idx fileIndex) (Result, error) {
	manifest := "Makefile"
	if idx.has("CMakeLists.txt") {
		manifest = "CMakeLists.txt"
	}
	res := withPort(baseResult(KindCC, "c", manifest, "C/C++ project"), DefaultPort, "pack")
	cfg, err := loadStartConfig(idx)
	if err != nil {
		return Result{}, err
	}
	res = applyStartPort(res, cfg)

	cmd := cfg.Start
	if len(cmd) == 0 {
		name, ok := cmakeBinary(idx)
		if !ok {
			return Result{}, needRunConfig("C/C++", "No single add_executable() in CMakeLists.txt, and no forge.json/Procfile start. ./app is not assumed.")
		}
		cmd = []string{"./" + name}
	}

	res.GeneratedDockerfile = ccImage(idx) + dockerfileCMD(cmd)
	return res, nil
}

func cmakeBinary(idx fileIndex) (string, bool) {
	if !idx.has("CMakeLists.txt") {
		return "", false
	}
	raw, err := idx.read("CMakeLists.txt")
	if err != nil {
		return "", false
	}
	ms := cmakeExec.FindAllSubmatch(raw, 4)
	if len(ms) != 1 {
		return "", false
	}
	name := ident(string(ms[0][1]))
	if name == "" {
		return "", false
	}
	return name, true
}

func ccImage(idx fileIndex) string {
	build := "RUN make\n"
	if idx.has("CMakeLists.txt") {
		build = "RUN cmake -B /tmp/build && cmake --build /tmp/build && cp /tmp/build/* /app/ 2>/dev/null || true\n"
	}
	return `FROM gcc:13
WORKDIR /app
COPY . .
` + build + `ENV PORT=8080
EXPOSE 8080
`
}
