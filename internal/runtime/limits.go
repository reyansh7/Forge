package runtime

import (
	"fmt"
	"strconv"
)

// Resources is the cgroup budget for one workload container.
//
// Zero means the historical default: 256 MiB, half a CPU, 256
// processes. Those defaults are what every existing deploy already
// ran with. A non-zero value is an operator override and is rejected
// outside the hard ceiling so one application cannot claim the host.
//
// Memory is MiB because Docker's --memory accepts a byte count and
// the dashboard should not ask operators to type bytes. CPU is
// milli-CPU (1000 = one core) so 0.5 CPU is the integer 500, not a
// float that drifts in JSON. PIDs are the --pids-limit cap.
const (
	DefaultMemoryMB  = 256
	DefaultCPUMillis = 500
	DefaultPids      = 256

	MinMemoryMB  = 64
	MaxMemoryMB  = 2048
	MinCPUMillis = 100
	MaxCPUMillis = 2000
	MinPids      = 32
	MaxPids      = 1024
)

// Resources is stored on the application. The next deploy reads it.
// A container that is already running keeps the budget it started with.
type Resources struct {
	MemoryMB  int
	CPUMillis int
	Pids      int
}

// Validate rejects values outside the ceiling. Zero is the default,
// not "unlimited".
func (r Resources) Validate() error {
	if r.MemoryMB != 0 && (r.MemoryMB < MinMemoryMB || r.MemoryMB > MaxMemoryMB) {
		return fmt.Errorf("memory_mb must be 0 or between %d and %d", MinMemoryMB, MaxMemoryMB)
	}
	if r.CPUMillis != 0 && (r.CPUMillis < MinCPUMillis || r.CPUMillis > MaxCPUMillis) {
		return fmt.Errorf("cpu_millis must be 0 or between %d and %d", MinCPUMillis, MaxCPUMillis)
	}
	if r.Pids != 0 && (r.Pids < MinPids || r.Pids > MaxPids) {
		return fmt.Errorf("pids_limit must be 0 or between %d and %d", MinPids, MaxPids)
	}
	return nil
}

// Custom reports whether the operator asked for something other than
// the default. A runner that cannot apply a custom budget must fail
// the deploy instead of silently starting at 256 MiB.
func (r Resources) Custom() bool {
	return r.MemoryMB != 0 || r.CPUMillis != 0 || r.Pids != 0
}

// Resolved replaces zeros with the defaults. It does not accept an
// invalid override; callers validate first.
func (r Resources) Resolved() Resources {
	if r.MemoryMB == 0 {
		r.MemoryMB = DefaultMemoryMB
	}
	if r.CPUMillis == 0 {
		r.CPUMillis = DefaultCPUMillis
	}
	if r.Pids == 0 {
		r.Pids = DefaultPids
	}
	return r
}

// resourceFlags is the Docker argv for the budget. --cpus is a decimal
// core count. 500 milli-CPU is "0.500", which Docker accepts and which
// stays exact for the allowed range.
func resourceFlags(r Resources) ([]string, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	got := r.Resolved()
	cpus := strconv.FormatFloat(float64(got.CPUMillis)/1000, 'f', 3, 64)
	return []string{
		"--memory", strconv.Itoa(got.MemoryMB) + "m",
		"--cpus", cpus,
		"--pids-limit", strconv.Itoa(got.Pids),
	}, nil
}
