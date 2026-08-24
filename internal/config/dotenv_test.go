package config

import "testing"

func TestParseDotEnvLineSkipsCommentsAndBlank(t *testing.T) {
	// Comments and blank lines must not become env keys (or syntax errors).
	_, _, ok, err := parseDotEnvLine("  # comment")
	if err != nil || ok {
		t.Fatalf("comment: ok=%v err=%v", ok, err)
	}
	_, _, ok, err = parseDotEnvLine("   ")
	if err != nil || ok {
		t.Fatalf("blank: ok=%v err=%v", ok, err)
	}
}

func TestParseDotEnvLineReadsKeyValue(t *testing.T) {
	key, val, ok, err := parseDotEnvLine("FORGE_REDIS_URL=redis://127.0.0.1:6379/0")
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	if key != "FORGE_REDIS_URL" || val != "redis://127.0.0.1:6379/0" {
		t.Fatalf("key=%q val=%q", key, val)
	}
}

func TestParseDotEnvLineRejectsMissingEquals(t *testing.T) {
	// A bare word is not a valid assignment; fail the file so the operator
	// notices a typo instead of silently skipping it.
	_, _, _, err := parseDotEnvLine("NOTAKEY")
	if err == nil {
		t.Fatal("expected syntax error")
	}
}
