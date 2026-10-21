// Package idmap makes datasets that appear inside running Incus containers
// after those containers started usable from inside them.
//
// Incus implements shift=true by giving every mount of a disk device an
// idmapped mount when the container starts. A dataset created later under a
// recursive device reaches the container through mount propagation instead,
// and a propagated copy carries no idmap: the container sees files owned by
// the host's root as nobody:nobody until it restarts. Repair clones the
// dataset's mount on the host, idmaps the clone with the container's user
// namespace and swaps it in for the propagated copy — the same mount Incus
// would have made at start.
package idmap

import (
	"fmt"
	"os/exec"
	"path"
	"strings"

	"github.com/tlvenn/zfs-provisioner/internal/mountcheck"
)

// Instance is a running Incus container.
type Instance struct {
	Project string
	Name    string
	Pid     int
	Devices map[string]map[string]string
}

func (i Instance) String() string {
	return i.Project + "/" + i.Name
}

// Target is a shifted, recursive disk device through which a host directory
// shows up inside a container.
type Target struct {
	Instance Instance
	Device   string
	Source   string // host path bind-mounted by the device
	Path     string // where the device mounts it inside the container
}

// ContainerPath maps a host path under the device's source to the path it
// has inside the container.
func (t Target) ContainerPath(hostPath string) string {
	return path.Join(t.Path, strings.TrimPrefix(hostPath, t.Source))
}

// Available reports whether this host runs Incus.
func Available() bool {
	_, err := exec.LookPath("incus")
	return err == nil
}

// Targets returns the shifted, recursive disk devices of the given containers
// whose source contains hostPath. The device's own source is excluded: Incus
// idmaps that mount itself at start.
func Targets(instances []Instance, hostPath string) []Target {
	hostPath = path.Clean(hostPath)

	var targets []Target
	for _, inst := range instances {
		for name, dev := range inst.Devices {
			if dev["type"] != "disk" || dev["shift"] != "true" || dev["recursive"] != "true" {
				continue
			}
			source := dev["source"]
			if !strings.HasPrefix(source, "/") || dev["path"] == "" {
				continue
			}
			source = path.Clean(source)
			if !strings.HasPrefix(hostPath, strings.TrimSuffix(source, "/")+"/") {
				continue
			}
			targets = append(targets, Target{Instance: inst, Device: name, Source: source, Path: dev["path"]})
		}
	}
	return targets
}

// Repairer swaps idmapped mounts in for propagated ones.
type Repairer struct {
	ListInstances func() ([]Instance, error)
	HostMounts    func() ([]mountcheck.Entry, error)
	PidMounts     func(pid int) ([]mountcheck.Entry, error)
	// Swap mounts an idmapped clone of hostPath at ctrPath inside the mount
	// namespace of pid, detaching whatever is visible there first when
	// replace is set.
	Swap func(pid int, hostPath, ctrPath string, replace bool) error
}

// NewRepairer returns a Repairer operating on this host.
func NewRepairer() *Repairer {
	return &Repairer{
		ListInstances: ListInstances,
		HostMounts:    mountcheck.Entries,
		PidMounts:     mountcheck.EntriesOf,
		Swap:          swap,
	}
}

// Instances lists the running containers whose mounts Repair may fix.
func (r *Repairer) Instances() ([]Instance, error) {
	return r.ListInstances()
}

// Repair makes the dataset mounted at hostMountpoint appear idmapped inside
// every given container whose shifted, recursive disk device covers it. Any
// dataset mounted between the device's source and hostMountpoint is fixed
// too, since zfs create -p mounts implicit parents that propagate the same
// way. It returns each container path it fixed, as "project/name:path".
func (r *Repairer) Repair(instances []Instance, hostMountpoint string) ([]string, error) {
	targets := Targets(instances, hostMountpoint)
	if len(targets) == 0 {
		return nil, nil
	}

	hostEntries, err := r.HostMounts()
	if err != nil {
		return nil, fmt.Errorf("reading host mounts: %w", err)
	}

	var fixed []string
	for _, t := range targets {
		pid := t.Instance.Pid
		ctrEntries, err := r.PidMounts(pid)
		if err != nil {
			return fixed, fmt.Errorf("%s: reading mounts: %w", t.Instance, err)
		}

		// Walk from just below the device's source down to the dataset, so a
		// parent is swapped in (carrying its children) before them.
		hostPath := t.Source
		for _, comp := range strings.Split(strings.TrimPrefix(path.Clean(hostMountpoint), t.Source+"/"), "/") {
			hostPath = path.Join(hostPath, comp)

			host := mountcheck.Visible(hostEntries, hostPath)
			if host == nil || host.FSType != "zfs" {
				continue // a plain directory, nothing mounted here on the host
			}

			ctrPath := t.ContainerPath(hostPath)
			ctr := mountcheck.Visible(ctrEntries, ctrPath)
			if ctr != nil && ctr.FSType == "zfs" && ctr.Source == host.Source && ctr.Idmapped() {
				continue
			}
			if ctr != nil && (ctr.FSType != "zfs" || ctr.Source != host.Source) {
				return fixed, fmt.Errorf("%s: expected dataset %s at %s but found %s (%s) mounted there",
					t.Instance, host.Source, ctrPath, ctr.Source, ctr.FSType)
			}
			if ctr == nil {
				// Nothing propagated: mounting the dataset would hide anything
				// the container already wrote to the bare directory.
				if err := mountcheck.VerifyDirEmptyForMount(host.Source, fmt.Sprintf("/proc/%d/root%s", pid, ctrPath)); err != nil {
					return fixed, fmt.Errorf("%s: %w", t.Instance, err)
				}
			}

			if err := r.Swap(pid, hostPath, ctrPath, ctr != nil); err != nil {
				return fixed, fmt.Errorf("%s: idmapping %s at %s: %w", t.Instance, host.Source, ctrPath, err)
			}

			if ctrEntries, err = r.PidMounts(pid); err != nil {
				return fixed, fmt.Errorf("%s: reading mounts: %w", t.Instance, err)
			}
			ctr = mountcheck.Visible(ctrEntries, ctrPath)
			if ctr == nil || ctr.Source != host.Source || !ctr.Idmapped() {
				return fixed, fmt.Errorf("%s: %s is still not idmapped at %s after the swap", t.Instance, host.Source, ctrPath)
			}
			if !ctr.Slave() {
				return fixed, fmt.Errorf("%s: %s at %s is not a slave of the host's mount, so the container's mount events would reach the host", t.Instance, host.Source, ctrPath)
			}
			fixed = append(fixed, t.Instance.String()+":"+ctrPath)
		}
	}

	return fixed, nil
}
