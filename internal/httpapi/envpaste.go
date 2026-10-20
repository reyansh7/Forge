package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

// envPasteRequest is one empty-or-filled paste per service directory.
// Text is parsed on the server. Values are not written to the log or
// the response.
type envPasteRequest struct {
	Services []envPasteService `json:"services"`
}

type envPasteService struct {
	Directory string `json:"directory"`
	Text      string `json:"text"`
}

type envPasteKey struct {
	Key   string `json:"key"`
	Class string `json:"class"`
}

type envPasteResult struct {
	Directory     string        `json:"directory"`
	ApplicationID string        `json:"application_id,omitempty"`
	Name          string        `json:"name,omitempty"`
	Saved         []envPasteKey `json:"saved"`
	Kept          []string      `json:"kept"`
	Issues        []string      `json:"issues"`
	Warnings      []string      `json:"warnings"`
}

// saveProjectEnv handles POST /projects/{id}/env.
//
// Each directory is one service. The paste is merged: keys in the text
// are written, and keys already stored but absent from the text stay.
// A blank paste does not delete anything. Reserved keys are rejected.
// The JSON response names keys only.
func (s *Server) saveProjectEnv(w http.ResponseWriter, r *http.Request) {
	if s.Apps == nil {
		writeError(w, http.StatusInternalServerError, "applications store is not configured")
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
	var req envPasteRequest
	if !decodeJSON(r, w, &req) {
		return
	}
	if len(req.Services) == 0 || len(req.Services) > 16 {
		writeError(w, http.StatusBadRequest, "paste one environment per service")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// The current application is any app in the project. Creating a
	// missing service copies its repository URL from that row.
	apps, err := s.Apps.ListApplicationsByProject(ctx, project.ID)
	if err != nil {
		s.logger().Error("list applications before env paste failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to save environment")
		return
	}
	var anchor store.Application
	if len(apps) > 0 {
		anchor = apps[0]
	} else {
		anchor = store.Application{ProjectID: project.ID, RepositoryURL: project.RepositoryURL, RootDirectory: "."}
	}
	out := make([]envPasteResult, 0, len(req.Services))
	for _, item := range req.Services {
		out = append(out, s.saveOneEnvPaste(ctx, &apps, anchor, item))
	}
	actor, _ := ActorFrom(r.Context())
	s.audit(r, actor.UserID, "project.env", "project", project.ID, map[string]string{
		"services": envPasteAudit(out),
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveOneEnvPaste(ctx context.Context, apps *[]store.Application, anchor store.Application, item envPasteService) envPasteResult {
	dir := strings.Trim(strings.TrimSpace(item.Directory), "/")
	if dir == "" {
		dir = "."
	}
	result := envPasteResult{Directory: dir, Saved: []envPasteKey{}, Kept: []string{}, Issues: []string{}, Warnings: []string{}}
	checked, err := store.ValidateRootDirectory(dir)
	if err != nil {
		result.Issues = append(result.Issues, "directory is not inside the repository")
		return result
	}
	if strings.TrimSpace(item.Text) == "" {
		result.Issues = append(result.Issues, "nothing to save")
		return result
	}
	vars, issues, err := store.ParseOperatorEnv(item.Text)
	for _, issue := range issues {
		result.Issues = append(result.Issues, issue.Text)
	}
	if err != nil {
		result.Issues = append(result.Issues, "environment text was rejected")
		return result
	}
	if len(vars) == 0 {
		return result
	}
	target, ferr := s.applicationForEnvPaste(ctx, apps, anchor, checked)
	if ferr != nil {
		s.logger().Error("match env service failed", "err", ferr)
		result.Issues = append(result.Issues, "could not match that service")
		return result
	}
	if err := s.Apps.MergeEnvVars(ctx, target.ID, vars); err != nil {
		s.logger().Error("merge env failed", "err", err, "application_id", target.ID)
		result.Issues = append(result.Issues, "could not save environment")
		return result
	}
	result.ApplicationID = target.ID
	result.Name = target.Name
	saved := map[string]struct{}{}
	for _, ev := range vars {
		saved[ev.Key] = struct{}{}
		result.Saved = append(result.Saved, envPasteKey{Key: ev.Key, Class: store.EnvVarClass(ev.Key)})
		if warn := store.PlaceholderWarning(ev.Key, ev.Value); warn != "" {
			result.Warnings = append(result.Warnings, warn)
		}
	}
	existing, err := s.Apps.ListEnvVars(ctx, target.ID)
	if err != nil {
		s.logger().Error("list env after merge failed", "err", err, "application_id", target.ID)
		return result
	}
	for _, ev := range existing {
		if _, ok := saved[ev.Key]; !ok {
			result.Kept = append(result.Kept, ev.Key)
		}
	}
	return result
}

func (s *Server) applicationForEnvPaste(ctx context.Context, apps *[]store.Application, anchor store.Application, dir string) (store.Application, error) {
	for _, app := range *apps {
		root := app.RootDirectory
		if root == "" {
			root = "."
		}
		if root == dir {
			return app, nil
		}
	}
	// A root paste must not attach to a nested service just because that
	// service was the first row. serviceForDirectory treats "." as "the
	// current application".
	if dir != "." && anchor.ID != "" {
		found, err := s.serviceForDirectory(ctx, anchor, dir)
		if err != nil {
			return store.Application{}, err
		}
		*apps = append(*apps, found)
		return found, nil
	}
	name := "app"
	if dir != "." {
		name = dir
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
	}
	created, err := s.Apps.CreateApplication(ctx, anchor.ProjectID, store.ApplicationInput{
		Name:          name,
		RepositoryURL: anchor.RepositoryURL,
		RootDirectory: dir,
		HealthPath:    "/",
	})
	if err != nil {
		return store.Application{}, err
	}
	*apps = append(*apps, created)
	return created, nil
}

func envPasteAudit(results []envPasteResult) string {
	var b strings.Builder
	for i, result := range results {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(result.Directory)
		b.WriteByte(':')
		for j, key := range result.Saved {
			if j > 0 {
				b.WriteByte('+')
			}
			b.WriteString(key.Key)
		}
	}
	return b.String()
}
