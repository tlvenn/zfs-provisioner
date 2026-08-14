package mountcheck

import (
	"strings"
	"testing"
)

const sampleMountinfo = `22 28 0:21 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
28 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
520 28 0:60 / /mnt rw,noatime shared:250 - zfs boot-pool/ROOT/25.10.5/mnt rw
601 520 0:81 / /mnt/nvme/seerr rw,relatime shared:310 - zfs nvme/seerr rw
645 520 0:99 / /mnt/nvme/radarr rw,relatime master:12 - zfs nvme/radarr rw
702 520 0:105 / /mnt/nvme/plain rw,relatime - zfs nvme/plain rw
810 520 0:120 / /mnt/with\040space rw shared:99 - zfs nvme/spaced rw
`

func parseSample(t *testing.T) []Entry {
	t.Helper()
	entries, err := parse(strings.NewReader(sampleMountinfo))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	return entries
}

func TestParse(t *testing.T) {
	entries := parseSample(t)

	if len(entries) != 7 {
		t.Fatalf("expected 7 entries, got %d", len(entries))
	}

	seerr := lookupExact(entries, "/mnt/nvme/seerr")
	if seerr == nil {
		t.Fatal("expected entry for /mnt/nvme/seerr")
	}
	if seerr.FSType != "zfs" || seerr.Source != "nvme/seerr" {
		t.Errorf("unexpected fstype/source: %s/%s", seerr.FSType, seerr.Source)
	}
	if !seerr.Shared() {
		t.Error("expected /mnt/nvme/seerr to be shared")
	}

	spaced := lookupExact(entries, "/mnt/with space")
	if spaced == nil {
		t.Error("expected octal-escaped mountpoint to be unescaped")
	}
}

func TestPropagation(t *testing.T) {
	entries := parseSample(t)

	cases := map[string]string{
		"/mnt/nvme/seerr":  "shared",
		"/mnt/nvme/radarr": "slave (rslave)",
		"/mnt/nvme/plain":  "private",
	}
	for mp, want := range cases {
		e := lookupExact(entries, mp)
		if e == nil {
			t.Fatalf("no entry for %s", mp)
		}
		if got := e.Propagation(); got != want {
			t.Errorf("%s: expected propagation %q, got %q", mp, want, got)
		}
	}
}

func TestLookupPath(t *testing.T) {
	entries := parseSample(t)

	// A path under a mounted dataset resolves to that dataset's mount.
	e := lookupPath(entries, "/mnt/nvme/seerr/db")
	if e == nil || e.Source != "nvme/seerr" {
		t.Errorf("expected nvme/seerr for /mnt/nvme/seerr/db, got %+v", e)
	}

	// A path with no dedicated mount falls back to the containing mount.
	e = lookupPath(entries, "/mnt/nvme/sonarr")
	if e == nil || e.Source != "boot-pool/ROOT/25.10.5/mnt" {
		t.Errorf("expected /mnt mount for /mnt/nvme/sonarr, got %+v", e)
	}

	// Prefix must match on path boundaries, not raw strings.
	e = lookupPath(entries, "/mnt/nvme/seerr-other")
	if e == nil || e.Source != "boot-pool/ROOT/25.10.5/mnt" {
		t.Errorf("expected /mnt mount for /mnt/nvme/seerr-other, got %+v", e)
	}

	// The exact mountpoint itself matches.
	e = lookupPath(entries, "/mnt")
	if e == nil || e.Source != "boot-pool/ROOT/25.10.5/mnt" {
		t.Errorf("expected /mnt mount for /mnt, got %+v", e)
	}
}

func TestLookupExactPicksLastOvermount(t *testing.T) {
	over := sampleMountinfo +
		"900 520 0:130 / /mnt/nvme/seerr rw shared:400 - zfs nvme/other rw\n"
	entries, err := parse(strings.NewReader(over))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	e := lookupExact(entries, "/mnt/nvme/seerr")
	if e == nil || e.Source != "nvme/other" {
		t.Errorf("expected last overmount (nvme/other) to win, got %+v", e)
	}
}

func TestSkipEnvVar(t *testing.T) {
	t.Setenv(SkipEnvVar, "1")

	if err := VerifyDatasetMounted("nvme/whatever", "/does/not/matter"); err != nil {
		t.Errorf("expected skip with %s set, got %v", SkipEnvVar, err)
	}
	if err := VerifyPropagation("/does/not/matter"); err != nil {
		t.Errorf("expected skip with %s set, got %v", SkipEnvVar, err)
	}
	if err := VerifyDirEmptyForMount("nvme/whatever", "/"); err != nil {
		t.Errorf("expected skip with %s set, got %v", SkipEnvVar, err)
	}
}

func TestVerifyDatasetMountedSkipsNonPathMountpoints(t *testing.T) {
	for _, mp := range []string{"legacy", "none", "-", ""} {
		if err := VerifyDatasetMounted("nvme/foo", mp); err != nil {
			t.Errorf("expected non-path mountpoint %q to be skipped, got %v", mp, err)
		}
	}
}
