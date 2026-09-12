package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreePrefersDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	write(t, filepath.Join(dir, "package.json"), `{}`)
	write(t, filepath.Join(dir, "go.mod"), "module example\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindDockerfile {
		t.Fatalf("kind = %q", got.Kind)
	}
	if got.GeneratedDockerfile != "" {
		t.Fatal("user Dockerfile must not be replaced")
	}
}

func TestTreeDetectsGoWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example\nrequire github.com/gin-gonic/gin v1.9.0\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindGo || got.Language != "go" {
		t.Fatalf("got %+v", got)
	}
	if got.Framework != "gin" {
		t.Fatalf("framework = %q", got.Framework)
	}
	if !strings.Contains(got.GeneratedDockerfile, "go build") {
		t.Fatal("missing go build recipe")
	}
	if got.ListenPort() != DefaultPort || got.PortSource != "pack" {
		t.Fatalf("port %+v", got)
	}
}

func TestTreeDetectsNodeWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"name":"x"}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindNode {
		t.Fatalf("kind = %q", got.Kind)
	}
	if got.GeneratedDockerfile != nodeGenericDockerfile {
		t.Fatal("generic node recipe changed")
	}
}

func TestTreeDetectsPythonWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "requirements.txt"), "fastapi==0.110.0\nuvicorn\n")
	write(t, filepath.Join(dir, "Procfile"), "web: uvicorn main:app --host 0.0.0.0 --port 8080\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindPython || got.Framework != "fastapi" {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(got.GeneratedDockerfile, "uvicorn") || strings.Contains(got.GeneratedDockerfile, "flask run") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreePythonRefusesGuessedEntrypoint(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "requirements.txt"), "flask\n")
	write(t, filepath.Join(dir, "app.py"), "app = None\n")
	_, err := Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "forge.json") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "flask run") || strings.Contains(strings.ToLower(err.Error()), "runserver") {
		t.Fatal(err)
	}
}

func TestTreeDetectsJavaWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pom.xml"), `<project><dependency>org.springframework.boot</dependency></project>`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindJava || got.Framework != "spring-boot" {
		t.Fatalf("got %+v", got)
	}
}

func TestTreeDetectsRustWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Cargo.toml"), "[package]\nname = \"hello-api\"\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindRust {
		t.Fatalf("kind = %q", got.Kind)
	}
	if !strings.Contains(got.GeneratedDockerfile, "./target/release/hello-api") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeDetectsRubyWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Gemfile"), "source \"https://rubygems.org\"\ngem \"rails\"\n")
	if err := os.Mkdir(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "config", "puma.rb"), "port 8080\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindRuby || got.Framework != "rails" {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(got.GeneratedDockerfile, "puma") || strings.Contains(got.GeneratedDockerfile, "rails server") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeDetectsPHPWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "composer.json"), `{"require":{"laravel/framework":"^11.0"}}`)
	write(t, filepath.Join(dir, "forge.json"), `{"start":["php","artisan","octane:start"]}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindPHP || got.Framework != "laravel" {
		t.Fatalf("got %+v", got)
	}
	if strings.Contains(got.GeneratedDockerfile, "php -S") || strings.Contains(got.GeneratedDockerfile, `"php","-S"`) {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreePHPRefusesDevServer(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "composer.json"), `{"require":{"php":"^8.3"}}`)
	_, err := Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "php -S") {
		t.Fatalf("err = %v", err)
	}
}

func TestTreeDetectsDotnetWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Web.csproj"), `<Project Sdk="Microsoft.NET.Sdk.Web"></Project>`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindDotnet || got.Framework != "aspnet" {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(got.GeneratedDockerfile, "/out/Web.dll") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeDetectsCCWithoutDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "CMakeLists.txt"), "add_executable(server src/main.cpp)\n")
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "src", "main.cpp"), "int main(){return 0;}\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindCC {
		t.Fatalf("kind = %q", got.Kind)
	}
	if !strings.Contains(got.GeneratedDockerfile, `"./server"`) {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeCCRefusesAssumedAppBinary(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Makefile"), "all:\n\tcc -o whatever main.c\n")
	write(t, filepath.Join(dir, "main.c"), "int main(){return 0;}\n")
	_, err := Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "./app") {
		t.Fatalf("err = %v", err)
	}
}

func TestTreeUnsupportedLanguageWithDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "FROM alpine\nCMD [\"echo\",\"ok\"]\n")
	write(t, filepath.Join(dir, "main.f90"), "program x\nend\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindDockerfile {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestTreeUnknownRepository(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "README.md"), "# notes\n")
	_, err := Tree(dir)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Unable to determine a deployment strategy") {
		t.Fatalf("msg = %s", msg)
	}
	if !strings.Contains(msg, "README.md") {
		t.Fatalf("should list files: %s", msg)
	}
	if !strings.Contains(msg, "Add a Dockerfile") || !strings.Contains(msg, "forge.json") {
		t.Fatalf("msg = %s", msg)
	}
}

func TestTreeIgnoresAssetsWhenClassifying(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example\n")
	write(t, filepath.Join(dir, "logo.png"), "not-a-png")
	write(t, filepath.Join(dir, "demo.mp4"), "not-a-video")
	write(t, filepath.Join(dir, "Brand.woff2"), "font")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindGo {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestTreeDoesNotTreatAssetsAsCSource(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Makefile"), "all:\n")
	write(t, filepath.Join(dir, "photo.jpg"), "x")
	write(t, filepath.Join(dir, "README.md"), "docs\n")
	_, err := Tree(dir)
	if err == nil {
		t.Fatal("Makefile + images is not a C project")
	}
}

func TestTreeRejectsEmptyTree(t *testing.T) {
	if _, err := Tree(t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}

func TestTreeMultipleManifestsPrefersGoOverNode(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example\n")
	write(t, filepath.Join(dir, "package.json"), `{"name":"web"}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindGo {
		t.Fatalf("kind = %q (go.mod must win over package.json)", got.Kind)
	}
}

func TestTreeMultipleManifestsPrefersRubyOverNode(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Gemfile"), "gem \"rails\"\n")
	write(t, filepath.Join(dir, "package.json"), `{"name":"assets"}`)
	if err := os.Mkdir(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "config", "puma.rb"), "port 8080\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindRuby {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestTreeDockerfileWinsOverManifests(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "FROM python:3.12\nEXPOSE 9000\n")
	write(t, filepath.Join(dir, "requirements.txt"), "flask\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindDockerfile {
		t.Fatalf("kind = %q", got.Kind)
	}
	if got.Port != 9000 || got.PortSource != "dockerfile_expose" {
		t.Fatalf("port %+v", got)
	}
}

func TestTreeFrameworkNext(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"dependencies":{"next":"14.0.0"},"scripts":{"build":"next build"}}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "next" {
		t.Fatalf("framework = %q", got.Framework)
	}
	if !strings.Contains(got.GeneratedDockerfile, "next") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeMalformedDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "# comments only\n")
	_, err := Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "missing FROM") {
		t.Fatalf("err = %v", err)
	}
}

func TestTreeMalformedPackageJSONStillNode(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{not-json`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindNode || got.Framework != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestTreeEmptyDirectoryPath(t *testing.T) {
	if _, err := Tree(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseDockerfileExpose(t *testing.T) {
	port, ok := parseDockerfileExpose([]byte("FROM scratch\nEXPOSE 3000/tcp\nEXPOSE 80\n"))
	if !ok || port != 3000 {
		t.Fatalf("port=%d ok=%v", port, ok)
	}
	if _, ok := parseDockerfileExpose([]byte("FROM scratch\n")); ok {
		t.Fatal("no EXPOSE")
	}
}

func TestTreeDjangoUsesWSGINotRunserver(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "requirements.txt"), "Django==5.0\n")
	write(t, filepath.Join(dir, "manage.py"), "pass\n")
	if err := os.Mkdir(filepath.Join(dir, "mysite"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "mysite", "wsgi.py"), "application = None\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "django" {
		t.Fatalf("framework = %q", got.Framework)
	}
	if !strings.Contains(got.GeneratedDockerfile, "mysite.wsgi:application") {
		t.Fatal(got.GeneratedDockerfile)
	}
	if strings.Contains(got.GeneratedDockerfile, "runserver") {
		t.Fatal("runserver must not be used")
	}
}

func TestTreeForgeJSONSetsPortAndStart(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "requirements.txt"), "flask\n")
	write(t, filepath.Join(dir, "forge.json"), `{"port":9090,"start":["gunicorn","hello:app","--bind","0.0.0.0:9090"]}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9090 || got.PortSource != "forge_json" {
		t.Fatalf("port %+v", got)
	}
	if !strings.Contains(got.GeneratedDockerfile, "gunicorn") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestListenPortDefault(t *testing.T) {
	if (Result{}).ListenPort() != DefaultPort {
		t.Fatal("zero result must use DefaultPort")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
