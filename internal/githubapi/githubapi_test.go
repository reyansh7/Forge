package githubapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestEnvExampleVarsKeepsExampleValues(t *testing.T) {
	in := "# comment\nexport VITE_API_URL=\"http://localhost:4000\"\n\nFORGE_API_ADDR=127.0.0.1:8080 # local\nBAD KEY=no\n"
	got := EnvExampleVars(in)
	if len(got) != 2 || got[0].Key != "VITE_API_URL" || got[0].Value != "http://localhost:4000" {
		t.Fatalf("%+v", got)
	}
	if got[1].Key != "FORGE_API_ADDR" || got[1].Value != "127.0.0.1:8080" {
		t.Fatalf("%+v", got[1])
	}
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte("hook-secret"))
	_, _ = mac.Write(body)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifySignature("hook-secret", header, body) {
		t.Fatal("expected valid signature")
	}
	if VerifySignature("hook-secret", header, []byte(`{"ref":"other"}`)) {
		t.Fatal("tampered body must fail")
	}
	if VerifySignature("other-secret", header, body) {
		t.Fatal("wrong secret must fail")
	}
	if VerifySignature("hook-secret", "", body) {
		t.Fatal("missing header must fail")
	}
	if VerifySignature("", header, body) {
		t.Fatal("empty secret must fail")
	}
}

func TestParsePush(t *testing.T) {
	body := []byte(`{
		"ref":"refs/heads/main",
		"after":"abc1234deadbee",
		"repository":{"full_name":"octo/hello"},
		"head_commit":{"id":"abc1234deadbee","message":"fix n+1\nmore","author":{"name":"Ada"}}
	}`)
	p, err := ParsePush(body)
	if err != nil {
		t.Fatal(err)
	}
	if p.Repo != "octo/hello" || p.Branch != "main" || p.Deleted {
		t.Fatalf("got %+v", p)
	}
	if p.CommitSHA != "abc1234deadbee" || p.Author != "Ada" || p.Message != "fix n+1\nmore" {
		t.Fatalf("commit %+v", p)
	}
}

func TestParsePushDeletedAndTag(t *testing.T) {
	p, err := ParsePush([]byte(`{"ref":"refs/heads/main","after":"0000000","deleted":true,"repository":{"full_name":"octo/hello"}}`))
	if err != nil || !p.Deleted {
		t.Fatalf("deleted: %+v err=%v", p, err)
	}
	p, err = ParsePush([]byte(`{"ref":"refs/tags/v1","after":"abc","repository":{"full_name":"octo/hello"}}`))
	if err != nil || p.Branch != "" {
		t.Fatalf("tag must not be a branch: %+v err=%v", p, err)
	}
}
