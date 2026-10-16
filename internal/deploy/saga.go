package deploy

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/reyansh7/Forge/internal/detect"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

// sagaStore is optional. Postgres implements it. A store that does not
// is a single-application deploy: no project network, no held
// containers, no compensating rollback.
type sagaStore interface {
	SagaView(ctx context.Context, deploymentID string) (store.SagaView, error)
	MarkPromoted(ctx context.Context, deploymentID, previousID string) error
	CommitIfReady(ctx context.Context, deploymentID string) ([]store.Deployment, bool, error)
	RecordCompensation(ctx context.Context, serviceID, state, message string) error
	RestorePrevious(ctx context.Context, newID, previousID string) error
	SetBuildPackage(ctx context.Context, applicationID, pkg string) error
}

type joinedRunner interface {
	EnsureNetwork(ctx context.Context, name string) error
	RunJoined(ctx context.Context, image, containerName string, env []runtime.EnvPair, containerPort int, vols []runtime.VolumeMount, network, alias string) (runtime.Instance, error)
}

func (h Handler) sagaView(ctx context.Context, deploymentID string) (store.SagaView, error) {
	g, ok := h.Store.(sagaStore)
	if !ok {
		return store.SagaView{}, nil
	}
	return g.SagaView(ctx, deploymentID)
}

// dependenciesPending is true when a required predecessor is not live
// and not failed. Failed predecessors are not pending: the promotion
// gate refuses the switch instead of waiting forever.
func dependenciesPending(v store.SagaView) bool {
	if !v.InSaga {
		return false
	}
	self := detect.Service{Name: v.ServiceName}
	for _, sib := range v.Siblings {
		if sib.DeploymentID == "" && sib.Name == v.ServiceName {
			continue
		}
		if sib.Name == v.ServiceName {
			continue
		}
		if !detect.DependsOn(self, detect.Service{Name: sib.Name}) {
			continue
		}
		switch sib.Status {
		case string(store.StatusLive), string(store.StatusFailed), string(store.StatusStopped):
			continue
		default:
			return true
		}
	}
	return false
}

// exposePublic is false for backends, workers, and ML services unless
// the operator set local_host. Frontends stay on Caddy. The loopback
// publish used for health checks is separate and is not a public route.
func exposePublic(name, localHost string) bool {
	if strings.TrimSpace(localHost) != "" {
		return true
	}
	switch detect.ServiceRole(detect.Service{Name: name}) {
	case "backend", "worker", "ml":
		return false
	default:
		return true
	}
}

func serviceLinkURL(proxyBase string, tgt store.Application, listen int) string {
	if exposePublic(tgt.Name, tgt.LocalHost) {
		return store.ServiceHostURL(proxyBase, tgt)
	}
	slug := strings.TrimSpace(tgt.LocalHost)
	if slug == "" {
		slug = store.SuggestedLocalHost(tgt.Name, tgt.ID)
	}
	return store.InternalServiceURL(slug, listen)
}

// compensationOrder is reverse promotion order. A service with no
// promotion time is not undone. The latest switch is restored first.
func compensationOrder(v store.SagaView, failedDeploymentID string) []store.SagaSibling {
	var steps []store.SagaSibling
	for _, sib := range v.Siblings {
		if sib.DeploymentID == failedDeploymentID || sib.PromotedAt.IsZero() {
			continue
		}
		if sib.Compensation != "" {
			continue
		}
		steps = append(steps, sib)
	}
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].PromotedAt.After(steps[j].PromotedAt)
	})
	return steps
}

func (h Handler) compensate(ctx context.Context, failedDeploymentID string) {
	g, ok := h.Store.(sagaStore)
	if !ok {
		return
	}
	view, err := g.SagaView(ctx, failedDeploymentID)
	if err != nil || !view.InSaga {
		if err != nil {
			h.log().Error("compensation load failed", "deployment_id", failedDeploymentID, "err", err)
		}
		return
	}
	var problems []string
	for _, step := range compensationOrder(view, failedDeploymentID) {
		err := h.restorePromoted(ctx, step)
		state := "restored"
		msg := ""
		if err != nil {
			state = "failed"
			msg = err.Error()
			problems = append(problems, step.Name+": "+msg)
		}
		if recErr := g.RecordCompensation(ctx, step.ServiceID, state, msg); recErr != nil {
			h.log().Error("record compensation failed", "service", step.Name, "err", recErr)
			problems = append(problems, step.Name+": record compensation: "+recErr.Error())
		}
	}
	if len(problems) > 0 {
		h.log().Error("saga compensation incomplete", "deployment_id", failedDeploymentID, "err", strings.Join(problems, "; "))
	}
}

func (h Handler) restorePromoted(ctx context.Context, step store.SagaSibling) error {
	g := h.Store.(sagaStore)
	if step.PreviousDeploymentID == "" {
		return fmt.Errorf("no previous healthy release; refusing to start a replacement image because migrations and volumes are not reversed automatically")
	}
	prev, err := h.Store.GetDeployment(ctx, step.PreviousDeploymentID)
	if err != nil {
		return fmt.Errorf("load previous release: %w", err)
	}
	next, err := h.Store.GetDeployment(ctx, step.DeploymentID)
	if err != nil {
		return fmt.Errorf("load promoted release: %w", err)
	}
	// Inspect before any database or routing change. If the held
	// container is already gone, leave the promoted release in place
	// and record the failure. Starting a fresh image here can replay
	// migrations against a volume the new release already changed.
	st, err := h.Runner.Inspect(ctx, runtime.ResolveContainerName(prev.ID, prev.ContainerName))
	if err != nil || !st.Exists || !st.Running {
		return fmt.Errorf("previous container is not running; refusing to build a new rollback image because that can replay migrations")
	}
	if err := g.RestorePrevious(ctx, next.ID, prev.ID); err != nil {
		return fmt.Errorf("database restore failed: %w", err)
	}
	if err := h.restoreRoutes(ctx); err != nil {
		return fmt.Errorf("routing restore failed: %w", err)
	}
	if err := h.Runner.Stop(ctx, runtime.ResolveContainerName(next.ID, next.ContainerName)); err != nil {
		return fmt.Errorf("stop promoted container failed: %w", err)
	}
	return nil
}

func (h Handler) finishSaga(ctx context.Context, d store.Deployment, replaced []store.Deployment) {
	g, ok := h.Store.(sagaStore)
	if !ok {
		h.stopReplaced(ctx, replaced)
		return
	}
	view, err := g.SagaView(ctx, d.ID)
	if err != nil {
		h.log().Error("saga view after cutover failed", "deployment_id", d.ID, "err", err)
		return
	}
	if !view.InSaga {
		h.stopReplaced(ctx, replaced)
		return
	}
	prevID := ""
	if len(replaced) > 0 {
		prevID = replaced[0].ID
	}
	if err := g.MarkPromoted(ctx, d.ID, prevID); err != nil {
		h.log().Error("mark promoted failed", "deployment_id", d.ID, "err", err)
	}
	stops, committed, err := g.CommitIfReady(ctx, d.ID)
	if err != nil {
		h.log().Error("saga commit check failed", "deployment_id", d.ID, "err", err)
		return
	}
	if committed {
		h.stopReplaced(ctx, stops)
	}
}

var goPackagePattern = regexp.MustCompile(`\./cmd/[A-Za-z0-9_-]+`)

// RepairGoPackage returns the single Go command named by a build or
// detect error. Two commands is ambiguous and is not repaired. The
// repair is a control-plane build_package, not a change to the repository.
func RepairGoPackage(text string) (string, bool) {
	found := goPackagePattern.FindAllString(text, -1)
	uniq := map[string]struct{}{}
	var list []string
	for _, item := range found {
		if _, ok := uniq[item]; ok {
			continue
		}
		uniq[item] = struct{}{}
		list = append(list, item)
	}
	if len(list) != 1 {
		return "", false
	}
	if _, err := store.ValidateBuildPackage(list[0]); err != nil {
		return "", false
	}
	return list[0], true
}

func (h Handler) startContainer(ctx context.Context, d store.Deployment, app store.Application, env []runtime.EnvPair, port int, vols []runtime.VolumeMount) (runtime.Instance, store.SagaView, error) {
	view, err := h.sagaView(ctx, d.ID)
	if err != nil {
		return runtime.Instance{}, store.SagaView{}, err
	}
	if !view.InSaga {
		inst, err := h.Runner.Run(ctx, d.ImageName, runtime.WorkloadName(app.Name, d.ID), env, port, vols)
		return inst, view, err
	}
	jr, ok := h.Runner.(joinedRunner)
	if !ok {
		return runtime.Instance{}, view, fmt.Errorf("runner cannot join a project network")
	}
	network, err := store.ProjectNetworkName(view.ProjectID)
	if err != nil {
		return runtime.Instance{}, view, err
	}
	slug := strings.TrimSpace(app.LocalHost)
	if slug == "" {
		slug = store.SuggestedLocalHost(app.Name, app.ID)
	}
	if err := jr.EnsureNetwork(ctx, network); err != nil {
		return runtime.Instance{}, view, err
	}
	inst, err := jr.RunJoined(ctx, d.ImageName, runtime.WorkloadName(app.Name, d.ID), env, port, vols, network, slug)
	return inst, view, err
}

// listenPortFor returns the live listen port of an application when
// this store can see it. Zero means the caller should use the default.
