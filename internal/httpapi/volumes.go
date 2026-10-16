package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

// VolumeStore is the HTTP → application_volumes boundary.
// Docker create/mount stays on the worker. HTTP persists intent.
type VolumeStore interface {
	CreateApplicationVolume(ctx context.Context, applicationID, name, mountPath string) (store.ApplicationVolume, error)
	ListApplicationVolumes(ctx context.Context, applicationID string) ([]store.ApplicationVolume, error)
	GetApplicationVolume(ctx context.Context, applicationID, volumeID string) (store.ApplicationVolume, error)
	DeleteApplicationVolume(ctx context.Context, applicationID, volumeID string) error
}

// VolumeRuntime is the optional HTTP → docker volume rm boundary.
// Tests leave Runtime as a logs/stop stub; production HostDocker implements this.
type VolumeRuntime interface {
	RemoveVolume(ctx context.Context, name string) error
}

type createVolumeRequest struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
}

type volumeResponse struct {
	ID                string `json:"id"`
	ApplicationID     string `json:"application_id"`
	Name              string `json:"name"`
	MountPath         string `json:"mount_path"`
	DockerName        string `json:"docker_name"`
	PreviewDockerName string `json:"preview_docker_name"`
}

func volumeResponseFrom(v store.ApplicationVolume) volumeResponse {
	out := volumeResponse{
		ID:            v.ID,
		ApplicationID: v.ApplicationID,
		Name:          v.Name,
		MountPath:     v.MountPath,
	}
	if name, err := store.DockerVolumeName(v.ApplicationID, v.Name, store.EnvProduction); err == nil {
		out.DockerName = name
	}
	if name, err := store.DockerVolumeName(v.ApplicationID, v.Name, store.EnvPreview); err == nil {
		out.PreviewDockerName = name
	}
	return out
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) {
	if s.Volumes == nil {
		writeError(w, http.StatusInternalServerError, "volumes are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	var req createVolumeRequest
	if !decodeJSON(r, w, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	v, err := s.Volumes.CreateApplicationVolume(ctx, app.ID, req.Name, req.MountPath)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "volume name or mount path already exists")
		return
	}
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "volume name") || strings.Contains(msg, "mount path") || strings.Contains(msg, "at most") || strings.Contains(msg, "overlaps") || strings.Contains(msg, "reserved") {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		s.logger().Error("create application volume failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create volume")
		return
	}
	s.audit(r, "", "application.volume.create", "application", app.ID, map[string]string{
		"name":       v.Name,
		"mount_path": v.MountPath,
	})
	writeJSON(w, http.StatusCreated, volumeResponseFrom(v))
}

func (s *Server) listVolumes(w http.ResponseWriter, r *http.Request) {
	if s.Volumes == nil {
		writeError(w, http.StatusInternalServerError, "volumes are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	vols, err := s.Volumes.ListApplicationVolumes(ctx, app.ID)
	if err != nil {
		s.logger().Error("list application volumes failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list volumes")
		return
	}
	out := make([]volumeResponse, 0, len(vols))
	for _, v := range vols {
		out = append(out, volumeResponseFrom(v))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	if s.Volumes == nil {
		writeError(w, http.StatusInternalServerError, "volumes are not configured")
		return
	}
	app, ok := s.loadApplication(w, r)
	if !ok {
		return
	}
	volumeID, err := store.ParseUUID(r.PathValue("volumeId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid volume id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	// Either environment can hold a named volume open (preview has
	// its own Docker name). Deleting while LIVE or mid-roll would
	// either fail `docker volume rm` as in-use or, worse, succeed
	// after cutover and destroy files the next container still needs.
	liveRows, err := s.liveDeployments(ctx, app.ID)
	if err != nil {
		s.logger().Error("list deployments before volume delete failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete volume")
		return
	}
	if len(liveRows) > 0 {
		writeError(w, http.StatusConflict, "stop live environments before deleting a volume")
		return
	}
	vol, err := s.Volumes.GetApplicationVolume(ctx, app.ID, volumeID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "volume not found")
		return
	}
	if err != nil {
		s.logger().Error("get application volume failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete volume")
		return
	}
	if err := s.removeDockerVolume(ctx, vol); err != nil {
		if strings.Contains(err.Error(), "in use") {
			writeError(w, http.StatusConflict, "volume is in use")
			return
		}
		s.logger().Error("docker volume rm failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete volume")
		return
	}
	if err := s.Volumes.DeleteApplicationVolume(ctx, app.ID, volumeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "volume not found")
			return
		}
		s.logger().Error("delete application volume failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete volume")
		return
	}
	s.audit(r, "", "application.volume.delete", "application", app.ID, map[string]string{"volume_id": volumeID, "name": vol.Name})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeDockerVolume(ctx context.Context, v store.ApplicationVolume) error {
	names, err := store.DockerVolumeNames(v.ApplicationID, v.Name)
	if err != nil {
		return err
	}
	rt, ok := s.Runtime.(VolumeRuntime)
	if !ok || rt == nil {
		return nil
	}
	for _, name := range names {
		if err := rt.RemoveVolume(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) removeApplicationVolumes(ctx context.Context, applicationID string) {
	if s.Volumes == nil {
		return
	}
	vols, err := s.Volumes.ListApplicationVolumes(ctx, applicationID)
	if err != nil {
		s.logger().Error("list volumes before application delete failed", "err", err)
		return
	}
	for _, v := range vols {
		if err := s.removeDockerVolume(ctx, v); err != nil {
			s.logger().Error("docker volume rm after application delete failed", "err", err, "name", v.Name)
		}
	}
}

// volumePreferNode is the placement hint for apps with persistent disk.
//
// HTTP does not pick a node. It only asks "did this environment
// already have a volume on some node?" Scheduler PlacePrefer decides.
// Preview and production names are isolated, so affinity is per
// environment — a preview volume on node B must not pull production
// onto B just because the app has a volume row.
func (s *Server) volumePreferNode(ctx context.Context, applicationID, environment string) string {
	if s == nil || s.Volumes == nil || s.Deployments == nil {
		return ""
	}
	vols, err := s.Volumes.ListApplicationVolumes(ctx, applicationID)
	if err != nil || len(vols) == 0 {
		return ""
	}
	list, err := s.Deployments.ListDeploymentsByApplication(ctx, applicationID)
	if err != nil {
		return ""
	}
	var fallback string
	for _, d := range list {
		if strings.TrimSpace(d.NodeID) == "" {
			continue
		}
		if !store.SameEnvironment(d.Environment, environment) {
			continue
		}
		if d.Status == store.StatusLive {
			return d.NodeID
		}
		if fallback == "" {
			fallback = d.NodeID
		}
	}
	return fallback
}
