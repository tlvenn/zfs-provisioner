// Package mountcheck validates mount propagation and mount visibility from
// the provisioner's mount namespace, guarding against the silent data-shadowing
// failure mode where datasets are mounted in a container namespace but never
// propagate to the host (or vice versa).
package mountcheck

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// SkipEnvVar bypasses all mount checks when set to a non-empty value.
const SkipEnvVar = "ZFS_SKIP_MOUNT_CHECKS"

// Entry represents one mount from /proc/self/mountinfo.
type Entry struct {
	MountPoint string
	FSType     string
	Source     string
	Optional   []string // propagation tags: "shared:N", "master:N", "unbindable"
}

// Shared reports whether the mount belongs to a shared peer group, meaning
// mounts created beneath it propagate to peers (e.g. the host namespace).
func (e *Entry) Shared() bool {
	for _, opt := range e.Optional {
		if strings.HasPrefix(opt, "shared:") {
			return true
		}
	}
	return false
}

// Propagation returns a human-readable propagation mode for error messages.
func (e *Entry) Propagation() string {
	var shared, slave bool
	for _, opt := range e.Optional {
		switch {
		case strings.HasPrefix(opt, "shared:"):
			shared = true
		case strings.HasPrefix(opt, "master:"):
			slave = true
		}
	}
	switch {
	case shared && slave:
		return "shared+slave"
	case shared:
		return "shared"
	case slave:
		return "slave (rslave)"
	default:
		return "private"
	}
}

// InContainer reports whether the process appears to run inside a container.
func InContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

func skipRequested() bool {
	return os.Getenv(SkipEnvVar) != ""
}

// Entries reads and parses /proc/self/mountinfo. On systems without procfs
// (e.g. during development on macOS) it returns nil, nil so checks degrade
// to no-ops.
func Entries() ([]Entry, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	return parse(f)
}

// parse reads mountinfo lines. Format per proc(5):
//
//	36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw
//	(1)(2)(3)  (4)   (5)   (6)        (7...)  (8)(9)  (10)      (11)
//
// Optional fields (7) run until the "-" separator.
func parse(r io.Reader) ([]Entry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	var entries []Entry
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}

		sep := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sep = i
				break
			}
		}
		if sep == -1 || sep+2 >= len(fields) {
			continue
		}

		entries = append(entries, Entry{
			MountPoint: unescape(fields[4]),
			Optional:   fields[6:sep],
			FSType:     fields[sep+1],
			Source:     unescape(fields[sep+2]),
		})
	}

	return entries, nil
}

// unescape decodes the octal escapes mountinfo uses for special characters.
func unescape(s string) string {
	for from, to := range map[string]string{`\040`: " ", `\011`: "\t", `\012`: "\n", `\134`: `\`} {
		s = strings.ReplaceAll(s, from, to)
	}
	return s
}

// lookupPath returns the mount entry containing path: the longest mount-point
// prefix match. Among equal mount points the last entry wins (later mounts
// shadow earlier ones).
func lookupPath(entries []Entry, path string) *Entry {
	var best *Entry
	bestLen := -1
	for i := range entries {
		mp := entries[i].MountPoint
		if mp != "/" && !strings.HasSuffix(mp, "/") {
			mp += "/"
		}
		if entries[i].MountPoint == path || strings.HasPrefix(path+"/", mp) {
			if len(entries[i].MountPoint) >= bestLen {
				best = &entries[i]
				bestLen = len(entries[i].MountPoint)
			}
		}
	}
	return best
}

// lookupExact returns the last mount entry whose mount point equals path.
func lookupExact(entries []Entry, path string) *Entry {
	var found *Entry
	for i := range entries {
		if entries[i].MountPoint == path {
			found = &entries[i]
		}
	}
	return found
}

// VerifyPropagation ensures that, when running inside a container, mounts
// created under parentMountpoint will propagate back to the host. Without
// shared propagation, datasets mounted by the provisioner exist only in the
// container's namespace: applications then write to the underlying directory
// on the host, and that data is shadowed the next time the host mounts the
// dataset (typically at reboot).
func VerifyPropagation(parentMountpoint string) error {
	if skipRequested() || !InContainer() {
		return nil
	}

	entries, err := Entries()
	if err != nil || entries == nil {
		return err
	}

	entry := lookupPath(entries, parentMountpoint)
	if entry == nil {
		return nil
	}

	if entry.Shared() {
		return nil
	}

	return fmt.Errorf(
		"refusing to provision: %s has %q mount propagation inside this container, so datasets mounted here will NOT be visible on the host — applications would write to the underlying directory and the data would be shadowed at the next host mount (e.g. reboot).\n"+
			"Fix: use rshared on the provisioner's volume, e.g.\n"+
			"    volumes:\n"+
			"      - %s:%s:rshared\n"+
			"(rslave only propagates host->container and is meant for remote mode, where the ZFS host does the mounting.)\n"+
			"Set %s=1 to bypass this check.",
		parentMountpoint, entry.Propagation(), parentMountpoint, parentMountpoint, SkipEnvVar)
}

// VerifyDatasetMounted ensures the dataset is what is actually mounted at its
// mountpoint in this namespace. It catches both a failed automount and the
// shadowed state where the path resolves to a plain directory on another
// filesystem. Non-path mountpoints (legacy, none, -) are skipped.
func VerifyDatasetMounted(dataset, mountpoint string) error {
	if skipRequested() || !strings.HasPrefix(mountpoint, "/") {
		return nil
	}

	entries, err := Entries()
	if err != nil || entries == nil {
		return err
	}

	entry := lookupExact(entries, mountpoint)
	if entry == nil {
		return fmt.Errorf(
			"dataset %s is not mounted at %s in this namespace: writes to that path would land on the underlying filesystem and be shadowed once the dataset mounts.\n"+
				"Check the provisioner's mount propagation (local mode needs rshared) or mount the dataset on the host. Set %s=1 to bypass this check.",
			dataset, mountpoint, SkipEnvVar)
	}

	if entry.FSType != "zfs" || entry.Source != dataset {
		return fmt.Errorf(
			"expected dataset %s at %s but found %s (%s) mounted there instead. Set %s=1 to bypass this check.",
			dataset, mountpoint, entry.Source, entry.FSType, SkipEnvVar)
	}

	return nil
}

// VerifyDirEmptyForMount ensures a would-be mountpoint directory does not
// already contain data that mounting a new dataset would shadow. A missing or
// empty directory is fine.
func VerifyDirEmptyForMount(dataset, dir string) error {
	if skipRequested() || !strings.HasPrefix(dir, "/") {
		return nil
	}

	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()

	names, err := f.Readdirnames(1)
	if err != nil || len(names) == 0 {
		return nil
	}

	return fmt.Errorf(
		"refusing to create %s: its mountpoint %s already contains data, which would be shadowed by the new dataset's mount. This usually means an application wrote there while the dataset did not exist or was not mounted — move that data aside first. Set %s=1 to bypass this check.",
		dataset, dir, SkipEnvVar)
}
