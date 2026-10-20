package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/reyansh7/Forge/internal/store"
)

func TestSaveProjectEnvIsolatesServicesAndHidesSecrets(t *testing.T) {
	secret := "hunter2-paste-secret"
	projects := newMemProjects()
	apps := newMemApps()
	project, err := projects.CreateProject(context.Background(), testUserID, store.ProjectInput{
		Name:          "MajorTesr",
		RepositoryURL: "https://github.com/example/app.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := apps.CreateApplication(context.Background(), project.ID, store.ApplicationInput{
		Name: "client", RepositoryURL: project.RepositoryURL, RootDirectory: "client", HealthPath: "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := apps.CreateApplication(context.Background(), project.ID, store.ApplicationInput{
		Name: "api", RepositoryURL: project.RepositoryURL, RootDirectory: "server", HealthPath: "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := apps.PutEnvVar(context.Background(), server.ID, "DB_PASSWORD", secret); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"services": []map[string]string{
			{"directory": "client", "text": "# note\nVITE_API_URL=/api\nVITE_API_URL=/api\nPORT=5000\nnot valid " + secret + "\nPLACE=changeme\n"},
			{"directory": "server", "text": ""},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := payload
	srv := appServer(projects, apps, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+project.ID+"/env", bytes.NewReader(body))
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), "changeme") {
		t.Fatal("response contained a secret or placeholder value")
	}
	var got []envPasteResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ApplicationID != client.ID {
		t.Fatalf("%+v", got)
	}
	clientEnv, err := apps.ListEnvVars(context.Background(), client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clientEnv) != 2 {
		t.Fatalf("client env %#v", clientEnv)
	}
	serverEnv, err := apps.ListEnvVars(context.Background(), server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(serverEnv) != 1 || serverEnv[0].Key != "DB_PASSWORD" || serverEnv[0].Value != secret {
		t.Fatalf("server env changed %#v", serverEnv)
	}
	for _, ev := range clientEnv {
		if ev.Key == "DB_PASSWORD" {
			t.Fatal("client received the server secret")
		}
	}
}
