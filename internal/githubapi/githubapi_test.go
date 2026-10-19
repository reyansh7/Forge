package githubapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestPartitionRootEnvSplitsClientAndServer(t *testing.T) {
	root := EnvExampleVars(`
NODE_ENV=production
PORT=5000
API_HOST=0.0.0.0
CLIENT_PORT=3000
VITE_API_URL=/api
DB_PASSWORD=db-secret
POSTGRES_PASSWORD=db-secret
`)
	got := partitionEnvTemplates(
		[]EnvTemplate{{Path: ".env.example", Directory: ".", Vars: root}},
		[]string{"client", "server", "db"},
	)
	if len(got) != 2 {
		t.Fatalf("client and server forms, got %+v", got)
	}
	if got[0].Directory != "client" || got[0].Source != ".env.example" {
		t.Fatalf("client: %+v", got[0])
	}
	if got[1].Directory != "server" || got[1].Source != ".env.example" {
		t.Fatalf("server: %+v", got[1])
	}
	clientKeys := map[string]bool{}
	for _, v := range got[0].Vars {
		clientKeys[v.Key] = true
		if strings.Contains(v.Key, "PASSWORD") || strings.HasPrefix(v.Key, "DB_") || strings.HasPrefix(v.Key, "POSTGRES_") {
			t.Fatalf("database key on client: %s", v.Key)
		}
	}
	if !clientKeys["VITE_API_URL"] || !clientKeys["CLIENT_PORT"] || clientKeys["PORT"] {
		t.Fatalf("client keys: %+v", got[0].Vars)
	}
	serverKeys := map[string]bool{}
	for _, v := range got[1].Vars {
		serverKeys[v.Key] = true
		if isFrontendEnvKey(v.Key) {
			t.Fatalf("frontend key on server: %s", v.Key)
		}
	}
	if !serverKeys["DB_PASSWORD"] || !serverKeys["POSTGRES_PASSWORD"] || !serverKeys["PORT"] {
		t.Fatalf("server keys: %+v", got[1].Vars)
	}
}

func TestPartitionKeepsAFolderFile(t *testing.T) {
	got := partitionEnvTemplates([]EnvTemplate{
		{Path: ".env.example", Directory: ".", Vars: []EnvExampleVar{{Key: "VITE_API_URL", Value: "/api"}, {Key: "PORT", Value: "5000"}}},
		{Path: "client/.env.example", Directory: "client", Vars: []EnvExampleVar{{Key: "VITE_API_URL", Value: "https://app.example"}}},
	}, []string{"client", "server"})
	var client EnvTemplate
	for _, row := range got {
		if row.Directory == "client" {
			client = row
		}
		if row.Directory == "server" {
			for _, v := range row.Vars {
				if v.Key == "VITE_API_URL" {
					t.Fatal("browser key must stay out of the server form")
				}
			}
		}
	}
	if client.Source != "" || client.Path != "client/.env.example" || len(client.Vars) != 1 || client.Vars[0].Value != "https://app.example" {
		t.Fatalf("folder file must win: %+v", client)
	}
}

func TestPartitionDoesNotGuessTwoFrontends(t *testing.T) {
	found := []EnvTemplate{{Path: ".env.example", Directory: ".", Vars: []EnvExampleVar{{Key: "VITE_API_URL", Value: "/api"}}}}
	got := partitionEnvTemplates(found, []string{"client", "web"})
	if len(got) != 1 || got[0].Directory != "." {
		t.Fatalf("ambiguous frontends stay on the root file: %+v", got)
	}
}

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
