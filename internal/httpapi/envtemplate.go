package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/githubapi"
	"github.com/reyansh7/Forge/internal/store"
)

// envTemplateAPI is optional. *githubapi.Client implements it. Tests
// that stub GitHub for OAuth do not have to.
type envTemplateAPI interface {
	EnvTemplates(ctx context.Context, token, repo, ref string) ([]githubapi.EnvTemplate, error)
}

type applyEnvTemplateRequest struct {
	Directory string         `json:"directory"`
	Vars      []store.EnvVar `json:"vars"`
}

type applyEnvTemplateResponse struct {
	ApplicationID string `json:"application_id"`
	Name          string `json:"name"`
	RootDirectory string `json:"root_directory"`
}

func (s *Server) listEnvTemplates(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	actor, _ := ActorFrom(r.Context())
	repo, ref, okRepo := githubRepoRef(app, store.ApplicationGitHub{})
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if s.GitHubAccounts != nil {
		if b, err := s.GitHubAccounts.GetApplicationGitHub(ctx, app.ID); err == nil && b.RepoFullName != "" {
			repo, ref, okRepo = githubRepoRef(app, b)
		}
	}
	if !okRepo {
		writeJSON(w, http.StatusOK, []githubapi.EnvTemplate{})
		return
	}
	src, okAPI := s.githubAPI().(envTemplateAPI)
	if !okAPI || s.GitHubAccounts == nil {
		writeJSON(w, http.StatusOK, []githubapi.EnvTemplate{})
		return
	}
	acc, err := s.GitHubAccounts.GetGitHubAccount(ctx, actor.UserID)
	if err != nil {
		writeJSON(w, http.StatusOK, []githubapi.EnvTemplate{})
		return
	}
	templates, err := src.EnvTemplates(ctx, acc.AccessToken, repo, ref)
	if err != nil {
		s.logger().Error("list env templates failed", "err", err)
		writeError(w, http.StatusBadGateway, "could not read env examples from GitHub")
		return
	}
	if templates == nil {
		templates = []githubapi.EnvTemplate{}
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) applyEnvTemplate(w http.ResponseWriter, r *http.Request) {
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	var req applyEnvTemplateRequest
	if !decodeJSON(r, w, &req) {
		return
	}
	dir := strings.Trim(strings.TrimSpace(req.Directory), "/")
	if dir == "" {
		dir = "."
	}
	checked, err := store.ValidateRootDirectory(dir)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	target := app
	if checked != "." && checked != app.RootDirectory {
		found, err := s.serviceForDirectory(ctx, app, checked)
		if err != nil {
			s.logger().Error("match env service failed", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to save environment")
			return
		}
		target = found
	}
	for _, v := range req.Vars {
		if strings.TrimSpace(v.Value) == "" {
			continue
		}
		if err := s.Apps.PutEnvVar(ctx, target.ID, v.Key, v.Value); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, applyEnvTemplateResponse{
		ApplicationID: target.ID,
		Name:          target.Name,
		RootDirectory: target.RootDirectory,
	})
}

func (s *Server) serviceForDirectory(ctx context.Context, current store.Application, dir string) (store.Application, error) {
	apps, err := s.Apps.ListApplicationsByProject(ctx, current.ProjectID)
	if err != nil {
		return store.Application{}, err
	}
	for _, a := range apps {
		if a.RootDirectory == dir {
			return a, nil
		}
	}
	name := dir
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return s.Apps.CreateApplication(ctx, current.ProjectID, store.ApplicationInput{
		Name:          name,
		RepositoryURL: current.RepositoryURL,
		RootDirectory: dir,
		HealthPath:    "/",
	})
}

func githubRepoRef(app store.Application, binding store.ApplicationGitHub) (repo, ref string, ok bool) {
	if strings.TrimSpace(binding.RepoFullName) != "" {
		ref = strings.TrimSpace(binding.Branch)
		if ref == "" {
			ref = "main"
		}
		return binding.RepoFullName, ref, true
	}
	u, err := url.Parse(strings.TrimSpace(app.RepositoryURL))
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", "", false
	}
	path := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0] + "/" + parts[1], "main", true
}
