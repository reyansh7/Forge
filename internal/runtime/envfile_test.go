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
