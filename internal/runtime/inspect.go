package runtime

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// ContainerState is a snapshot of docker inspect for one workload.
//
// Health checks must look here first. A container that already exited
// cannot become healthy no matter how long we poll loopback.
type ContainerState struct {
	Name     string
	ID       string
	Running  bool
	Status   string
	ExitCode int
	Error    string
	Exists   bool
}

// Inspect reads State.Running / ExitCode / Status for a named container.
//
// Missing containers are Exists=false, not an error: a failed `docker
// run` often leaves no name, and the caller should report the run
// error instead of "inspect failed".
func (HostDocker) Inspect(ctx context.Context, containerName string) (ContainerState, error) {
	if err := validateContainerName(containerName); err != nil {
		return ContainerState{}, err
	}
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f",
		"{{.Id}} {{.State.Running}} {{.State.ExitCode}} {{.State.Status}} {{.State.Error}}",
		"--", containerName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ContainerState{Name: containerName, Exists: false}, nil
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	st := ContainerState{Name: containerName, Exists: true}
	if len(fields) >= 1 {
		st.ID = fields[0]
		if len(st.ID) > 12 {
			st.ID = st.ID[:12]
		}
	}
	if len(fields) >= 2 {
		st.Running = fields[1] == "true"
	}
	if len(fields) >= 3 {
		st.ExitCode, _ = strconv.Atoi(fields[2])
	}
	if len(fields) >= 4 {
		st.Status = fields[3]
	}
	if len(fields) >= 5 {
		st.Error = strings.Join(fields[4:], " ")
	}
	return st, nil
}
