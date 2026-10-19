package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reyansh7/Forge/internal/detect"
)

func TestMergeBuildIgnorePreservesUserRules(t *testing.T) {
	dir := t.TempDir()
	user := "npm-debug.log\n!data/keep.csv\n"
	if err := os.WriteFile(filepath.Join(dir, ".dockerignore"), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeBuildIgnore(dir, detect.KindNode, true); err != nil {
		t.Fatal(err)
	}
	if err := mergeBuildIgnore(dir, detect.KindNode, true); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Count(text, forgeIgnoreMarker) != 1 {
		t.Fatalf("marker count:\n%s", text)
	}
	if !strings.HasPrefix(text, user) && !strings.HasPrefix(text, strings.TrimSuffix(user, "\n")) {
		t.Fatalf("user rules were rewritten:\n%s", text)
	}
	for _, keep := range []string{"npm-debug.log", "!data/keep.csv", ".git", "node_modules", ".venv"} {
		if keep == ".venv" {
			if strings.Contains(text, ".venv") {
				t.Fatalf("python venv pattern on a node recipe:\n%s", text)
			}
			continue
		}
		if !strings.Contains(text, keep) {
			t.Fatalf("missing %q\n%s", keep, text)
		}
	}
	for _, banned := range []string{"ml-model", "saved_models", "*.csv", "*.joblib", "*.pdf", "assets", "dist", ".next"} {
		if strings.Contains(text, banned) {
			t.Fatalf("excluded required or generated output %q\n%s", banned, text)
		}
	}
}

func TestMergeBuildIgnoreUserDockerfileSkipsDependencyCaches(t *testing.T) {
	dir := t.TempDir()
	if err := mergeBuildIgnore(dir, detect.KindDockerfile, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, ".git") || !strings.Contains(text, "**/.git") {
		t.Fatal(text)
	}
	if strings.Contains(text, "node_modules") || strings.Contains(text, ".venv") || strings.Contains(text, "__pycache__") || strings.Contains(text, "target") || strings.Contains(text, ".gradle") {
		t.Fatalf("user Dockerfile context dropped reinstall caches:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		switch strings.TrimSpace(line) {
		case "bin", "obj":
			t.Fatalf("user Dockerfile context dropped %s:\n%s", line, text)
		}
	}
}

func TestPrepareDockerfileWritesNodeIgnore(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("console.log(1)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "resumes.csv"), []byte("id,name\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareDockerfile(dir, string(detect.KindNode)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "node_modules") || !strings.Contains(text, ".git") {
		t.Fatal(text)
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case "data", "data/", "resumes.csv", "ml-model", "saved_models", "assets", "*.csv", "*.joblib":
			t.Fatalf("dataset path was ignored: %q\n%s", line, text)
		}
	}
}

func TestRecipeIgnorePatterns(t *testing.T) {
	node := strings.Join(recipeIgnorePatterns(detect.KindNode), "\n")
	py := strings.Join(recipeIgnorePatterns(detect.KindPython), "\n")
	if !strings.Contains(node, "node_modules") || strings.Contains(node, ".venv") {
		t.Fatal(node)
	}
	if !strings.Contains(py, ".venv") || !strings.Contains(py, "__pycache__") || strings.Contains(py, "node_modules") {
		t.Fatal(py)
	}
	if recipeIgnorePatterns(detect.KindGo) != nil {
		t.Fatal("go recipe must not drop module files")
	}
	if !strings.Contains(strings.Join(recipeIgnorePatterns(detect.KindRust), "\n"), "target") {
		t.Fatal("rust recipe must drop the cargo target directory")
	}
	java := strings.Join(recipeIgnorePatterns(detect.KindJava), "\n")
	if !strings.Contains(java, "target") || !strings.Contains(java, ".gradle") || strings.Contains(java, "\nbuild\n") {
		t.Fatal(java)
	}
	dotnet := strings.Join(recipeIgnorePatterns(detect.KindDotnet), "\n")
	if !strings.Contains(dotnet, "bin") || !strings.Contains(dotnet, "obj") {
		t.Fatal(dotnet)
	}
}

func TestEveryRuntimeIgnoreKeepsAssets(t *testing.T) {
	banned := []string{
		"*.csv", "*.pdf", "*.png", "*.mp4", "*.parquet", "*.joblib",
		"data", "models", "assets", "ml-model", "saved_models",
		"go.mod", "go.sum", "vendor", "Cargo.toml", "Cargo.lock",
		"package.json", "requirements.txt", "pom.xml",
		"dist", ".next", "build",
	}
	for _, kind := range []detect.Kind{
		detect.KindNode, detect.KindPython, detect.KindGo,
		detect.KindJava, detect.KindRust, detect.KindDotnet, detect.KindDockerfile,
	} {
		for _, line := range recipeIgnorePatterns(kind) {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			for _, name := range banned {
				if line == name {
					t.Fatalf("%s ignore contains %q", kind, name)
				}
			}
		}
	}
}

func TestLargestContextBytes(t *testing.T) {
	n, ok := LargestContextBytes("#4 transferring context: 2.62GB 522.6s done\n#3 transferring context: 263B 0.0s done\n")
	if !ok || n != 2620000000 {
		t.Fatalf("got %d ok=%v", n, ok)
	}
	n, ok = LargestContextBytes("Sending build context to Docker daemon  2.048kB\n")
	if !ok || n != 2048 {
		t.Fatalf("classic got %d ok=%v", n, ok)
	}
	if _, ok := LargestContextBytes("build succeeded\n"); ok {
		t.Fatal("missing context line was reported as a size")
	}
	n, ok = LargestContextBytes("transferring context: 0B 0.0s done\n")
	if !ok || n != 0 {
		t.Fatalf("explicit zero got %d ok=%v", n, ok)
	}
}
