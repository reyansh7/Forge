package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/detect"
	"github.com/reyansh7/Forge/internal/githubapi"
	"github.com/reyansh7/Forge/internal/store"
)

// projectPlan lists services in the project's GitHub repository.
//
// It downloads manifests only, then runs the same detectors used at
// deploy time. It does not build or start anything. A repository that
// is not on GitHub, or an operator without a connected GitHub account,
// gets an empty list.
func (s *Server) projectPlan(w http.ResponseWriter, r *http.Request) {
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, "projects store is not configured")
		return
	}
	id, err := store.ParseProjectID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	project, ok := s.requireProject(w, r, id)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	actor, _ := ActorFrom(r.Context())
	services, err := s.discoverProjectServices(ctx, actor.UserID, project)
	if err != nil {
		s.logger().Error("discover failed", "err", err)
		writeError(w, http.StatusBadGateway, "could not read the repository from GitHub")
		return
	}
	if services == nil {
		services = []detect.Service{}
	}
	writeJSON(w, http.StatusOK, services)
}

// discoverProjectServices is the shared read of a repository plan.
//
// Discoverer is set by tests so they do not dial GitHub. Production
// downloads manifests with the operator's token and runs detect.Discover.
// A non-GitHub URL or a missing GitHub account is an empty plan, not an error.
func (s *Server) discoverProjectServices(ctx context.Context, actorID string, project store.Project) ([]detect.Service, error) {
	if s.Discoverer != nil {
		return s.Discoverer(ctx, project)
	}
	repo, ref := repoRefFromURL(project.RepositoryURL)
	if repo == "" {
		return []detect.Service{}, nil
	}
	src, isClient := s.githubAPI().(*githubapi.Client)
	if !isClient || s.GitHubAccounts == nil {
		return []detect.Service{}, nil
	}
	acc, err := s.GitHubAccounts.GetGitHubAccount(ctx, actorID)
	if err != nil {
		return []detect.Service{}, nil
	}
	dir, err := os.MkdirTemp("", "forge-plan")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := src.WriteDiscoverTree(ctx, acc.AccessToken, repo, ref, dir); err != nil {
		return nil, err
	}
	return detect.Discover(dir)
}

func repoRefFromURL(raw string) (repo, ref string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", ""
	}
	path := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return parts[0] + "/" + parts[1], "main"
}
