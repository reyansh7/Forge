package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatProductionEnvQuotesAndRejectsReserved(t *testing.T) {
	body, err := formatProductionEnv([]EnvPair{
		{Key: "VITE_APP_EMAILJS_PUBLIC_KEY", Value: "plain"},
		{Key: "VITE_NOTE", Value: `say "hi"`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "VITE_APP_EMAILJS_PUBLIC_KEY=plain\n") {
		t.Fatalf("%s", body)
	}
	if !strings.Contains(body, `VITE_NOTE="say \"hi\""`) {
		t.Fatalf("quoted = %s", body)
	}
	if _, err := formatProductionEnv([]EnvPair{{Key: "PORT", Value: "1"}}); err == nil {
		t.Fatal("PORT must stay reserved at build time too")
	}
}

func TestPublicBuildEnvOmitsSecretsAndChangesWithValue(t *testing.T) {
	env := []EnvPair{
		{Key: "DATABASE_URL", Value: "postgres://db/app"},
		{Key: "NEXT_PUBLIC_API_URL", Value: "http://api.localhost:9080/"},
		{Key: "VITE_API_URL", Value: "http://api.localhost:9080/"},
	}
	pub := PublicBuildEnv(env)
	if len(pub) != 2 {
		t.Fatalf("%+v", pub)
	}
	body, err := formatProductionEnv(pub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "DATABASE_URL") || strings.Contains(body, "postgres") {
		t.Fatalf("secret reached the build file: %s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "http://api.localhost:9080/") && !strings.Contains(body, "http://api.localhost:9080/") {
		t.Fatalf("%s", body)
	}
	changed := PublicBuildEnv([]EnvPair{{Key: "NEXT_PUBLIC_API_URL", Value: "http://api.localhost:9080/v2/"}})
	other, err := formatProductionEnv(changed)
	if err != nil {
		t.Fatal(err)
	}
	if body == other {
		t.Fatal("a new public URL must change the build file so the image layer cannot be reused")
	}
}

func TestWriteProductionEnvSkipsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := writeProductionEnv(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, productionEnvName)); !os.IsNotExist(err) {
		t.Fatal("empty env must not create a file")
	}
	if err := writeProductionEnv(dir, []EnvPair{{Key: "VITE_X", Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, productionEnvName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "VITE_X=1") {
		t.Fatalf("%s", raw)
	}
}
