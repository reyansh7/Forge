package store

import (
	"strings"
	"testing"
)

func TestParseOperatorEnvHidesValues(t *testing.T) {
	secret := "hunter2-do-not-leak"
	text := "# comment\n\n" +
		"export DB_PASSWORD=\"" + secret + "\"\n" +
		"URL='postgres://db?x=1'\n" +
		"PLAIN=a=b=c\n" +
		"TOKEN=" + secret + "\n" +
		"TOKEN=kept-second\n" +
		"not a valid line " + secret + "\n" +
		"PORT=5000\n" +
		"FORGE_API_ADDR=" + secret + "\n" +
		"1BAD=" + secret + "\n" +
		"PLACE=changeme-" + secret + "\n"
	vars, issues, err := ParseOperatorEnv(text)
	if err != nil {
		t.Fatal(err)
	}
	blob := joinIssues(issues)
	if strings.Contains(blob, secret) || strings.Contains(blob, "kept-second") || strings.Contains(blob, "postgres://") {
		t.Fatalf("issue text leaked a value: %s", blob)
	}
	got := map[string]string{}
	for _, ev := range vars {
		got[ev.Key] = ev.Value
	}
	if got["DB_PASSWORD"] != secret || got["URL"] != "postgres://db?x=1" || got["PLAIN"] != "a=b=c" {
		t.Fatalf("parsed %#v", got)
	}
	if got["TOKEN"] != "kept-second" {
		t.Fatalf("duplicate key = %q", got["TOKEN"])
	}
	if _, ok := got["PORT"]; ok {
		t.Fatal("reserved PORT was kept")
	}
	if _, ok := got["FORGE_API_ADDR"]; ok {
		t.Fatal("reserved FORGE_ key was kept")
	}
	if !strings.Contains(blob, "TOKEN was set more than once") || !strings.Contains(blob, "is not KEY=VALUE") || !strings.Contains(blob, "reserved") {
		t.Fatalf("issues %s", blob)
	}
	warn := PlaceholderWarning("PLACE", got["PLACE"])
	if strings.Contains(warn, secret) || !strings.Contains(warn, "PLACE") {
		t.Fatalf("warning %q", warn)
	}
	if EnvVarClass("VITE_API_URL") != "build" || EnvVarClass("DB_PASSWORD") != "runtime" {
		t.Fatal("class")
	}
}

func TestParseOperatorEnvIgnoresComments(t *testing.T) {
	vars, issues, err := ParseOperatorEnv("# KEY=secret\n\nVITE_API_URL=/api # public path\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(vars) != 1 || vars[0].Key != "VITE_API_URL" || vars[0].Value != "/api" {
		t.Fatalf("vars %#v issues %#v", vars, issues)
	}
}

func joinIssues(issues []EnvIssue) string {
	var b strings.Builder
	for _, issue := range issues {
		b.WriteString(issue.Text)
		b.WriteByte('\n')
	}
	return b.String()
}
