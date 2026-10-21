package idmap

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tlvenn/zfs-provisioner/internal/mountcheck"
)

func opsInstance() Instance {
	return Instance{
		Project: "magelo",
		Name:    "ops",
		Pid:     2434,
		Devices: map[string]map[string]string{
			"ops_data": {"type": "disk", "source": "/data/magelo/ops", "path": "/data", "recursive": "true", "shift": "true"},
			"root":     {"type": "disk", "pool": "magelo", "path": "/"},
		},
	}
}

func TestTargets(t *testing.T) {
	unshifted := Instance{Project: "magelo", Name: "plain", Pid: 1, Devices: map[string]map[string]string{
		"d": {"type": "disk", "source": "/data/magelo/ops", "path": "/data", "recursive": "true"},
	}}
	sibling := Instance{Project: "magelo", Name: "sibling", Pid: 2, Devices: map[string]map[string]string{
		"d": {"type": "disk", "source": "/data/magelo/opsx", "path": "/data", "recursive": "true", "shift": "true"},
	}}
	instances := []Instance{opsInstance(), unshifted, sibling}

	targets := Targets(instances, "/data/magelo/ops/traceway/data")
	if len(targets) != 1 || targets[0].Instance.Name != "ops" || targets[0].Device != "ops_data" {
		t.Fatalf("expected only ops/ops_data, got %+v", targets)
	}
	if got := targets[0].ContainerPath("/data/magelo/ops/traceway/data"); got != "/data/traceway/data" {
		t.Errorf("unexpected container path %q", got)
	}

	// The device's own source is idmapped by Incus at start.
	if targets := Targets(instances, "/data/magelo/ops"); len(targets) != 0 {
		t.Errorf("expected no target for the device source itself, got %+v", targets)
	}
}

func TestParseInstances(t *testing.T) {
	data := []byte(`[
		{"name": "ops", "project": "magelo", "type": "container", "status": "Running", "state": {"pid": 2434},
		 "expanded_devices": {"ops_data": {"type": "disk", "source": "/data/magelo/ops", "path": "/data"}}},
		{"name": "vm", "project": "magelo", "type": "virtual-machine", "status": "Running", "state": {"pid": 99}},
		{"name": "off", "project": "magelo", "type": "container", "status": "Stopped", "state": {"pid": 0}}
	]`)

	instances, err := parseInstances(data)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(instances) != 1 || instances[0].String() != "magelo/ops" || instances[0].Pid != 2434 {
		t.Fatalf("expected only the running container, got %+v", instances)
	}
	if instances[0].Devices["ops_data"]["source"] != "/data/magelo/ops" {
		t.Errorf("devices not carried over: %+v", instances[0].Devices)
	}
}

const hostMountinfo = `1 0 0:1 / / rw - zfs rpool/ROOT rw
10 1 0:10 / /data/magelo/ops rw shared:84 - zfs rpool/data/magelo/ops rw
20 10 0:20 / /data/magelo/ops/traceway rw shared:200 - zfs rpool/data/magelo/ops/traceway rw
21 20 0:21 / /data/magelo/ops/traceway/data rw shared:201 - zfs rpool/data/magelo/ops/traceway/data rw
`

// Container view right after zfs create -p: the implicit parent and the
// dataset both arrived through propagation, without an idmap.
const propagated = `100 99 0:50 / / rw - zfs rpool/data/incus/magelo/containers/ops rw
110 100 0:10 / /data rw,idmapped shared:594 master:84 - zfs rpool/data/magelo/ops rw
120 110 0:20 / /data/traceway rw shared:600 master:200 - zfs rpool/data/magelo/ops/traceway rw
121 120 0:21 / /data/traceway/data rw shared:601 master:201 - zfs rpool/data/magelo/ops/traceway/data rw
`

// After swapping in a recursive idmapped clone of traceway: shared inside
// the container, slave of the host's mounts.
const swapped = `100 99 0:50 / / rw - zfs rpool/data/incus/magelo/containers/ops rw
110 100 0:10 / /data rw,idmapped shared:594 master:84 - zfs rpool/data/magelo/ops rw
130 110 0:20 / /data/traceway rw,idmapped shared:700 master:200 - zfs rpool/data/magelo/ops/traceway rw
131 130 0:21 / /data/traceway/data rw,idmapped shared:701 master:201 - zfs rpool/data/magelo/ops/traceway/data rw
`

type swapCall struct {
	Pid     int
	Host    string
	Ctr     string
	Replace bool
}

func fakeRepairer(t *testing.T, ctr *string, after string) (*Repairer, *[]swapCall) {
	t.Helper()
	var calls []swapCall
	parse := func(s string) []mountcheck.Entry {
		entries, err := mountcheck.ParseMountinfo(strings.NewReader(s))
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		return entries
	}
	return &Repairer{
		HostMounts: func() ([]mountcheck.Entry, error) { return parse(hostMountinfo), nil },
		PidMounts:  func(pid int) ([]mountcheck.Entry, error) { return parse(*ctr), nil },
		Swap: func(pid int, hostPath, ctrPath string, replace bool) error {
			calls = append(calls, swapCall{pid, hostPath, ctrPath, replace})
			*ctr = after
			return nil
		},
	}, &calls
}

func TestRepairSwapsTopmostPropagatedMount(t *testing.T) {
	ctr := propagated
	r, calls := fakeRepairer(t, &ctr, swapped)

	fixed, err := r.Repair([]Instance{opsInstance()}, "/data/magelo/ops/traceway/data")
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}

	// One swap at the implicit parent: the recursive clone carries the
	// dataset along, so the walk finds it idmapped afterwards.
	want := []swapCall{{2434, "/data/magelo/ops/traceway", "/data/traceway", true}}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("expected swaps %+v, got %+v", want, *calls)
	}
	if !reflect.DeepEqual(fixed, []string{"magelo/ops:/data/traceway"}) {
		t.Errorf("unexpected fixed list %v", fixed)
	}

	// Idempotent: nothing left to do.
	*calls = nil
	fixed, err = r.Repair([]Instance{opsInstance()}, "/data/magelo/ops/traceway/data")
	if err != nil || len(fixed) != 0 || len(*calls) != 0 {
		t.Errorf("expected no-op on second run, got fixed=%v calls=%+v err=%v", fixed, *calls, err)
	}
}

func TestRepairMountsDatasetThatNeverPropagated(t *testing.T) {
	// The parent is an earlier idmapped clone (private), so a dataset created
	// under it later never shows up in the container at all.
	ctr := `100 99 0:50 / / rw - zfs rpool/data/incus/magelo/containers/ops rw
110 100 0:10 / /data rw,idmapped shared:594 master:84 - zfs rpool/data/magelo/ops rw
130 110 0:20 / /data/traceway rw,idmapped - zfs rpool/data/magelo/ops/traceway rw
`
	r, calls := fakeRepairer(t, &ctr, swapped)

	if _, err := r.Repair([]Instance{opsInstance()}, "/data/magelo/ops/traceway/data"); err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	want := []swapCall{{2434, "/data/magelo/ops/traceway/data", "/data/traceway/data", false}}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("expected swaps %+v, got %+v", want, *calls)
	}
}

func TestRepairRefusesForeignMount(t *testing.T) {
	ctr := strings.Replace(propagated, "zfs rpool/data/magelo/ops/traceway rw", "zfs tank/elsewhere rw", 1)
	r, calls := fakeRepairer(t, &ctr, swapped)

	_, err := r.Repair([]Instance{opsInstance()}, "/data/magelo/ops/traceway/data")
	if err == nil || !strings.Contains(err.Error(), "tank/elsewhere") {
		t.Fatalf("expected foreign mount error, got %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("expected no swap, got %+v", *calls)
	}
}

func TestRepairReportsSwapThatDidNotTakeEffect(t *testing.T) {
	ctr := propagated
	r, _ := fakeRepairer(t, &ctr, propagated)

	_, err := r.Repair([]Instance{opsInstance()}, "/data/magelo/ops/traceway/data")
	if err == nil || !strings.Contains(err.Error(), "still not idmapped") {
		t.Fatalf("expected verification error, got %v", err)
	}
}

func TestRepairIgnoresUncoveredPaths(t *testing.T) {
	ctr := propagated
	r, calls := fakeRepairer(t, &ctr, swapped)

	fixed, err := r.Repair([]Instance{opsInstance()}, "/tank/elsewhere/app")
	if err != nil || fixed != nil || len(*calls) != 0 {
		t.Errorf("expected nothing to do, got fixed=%v calls=%+v err=%v", fixed, *calls, err)
	}
}

func TestRepairRequiresSlaveOfHost(t *testing.T) {
	// A clone left in the host's peer group would send the container's mount
	// events back, e.g. unmount the host's datasets when the container stops.
	peer := strings.ReplaceAll(swapped, "shared:700 master:200", "shared:200")
	ctr := propagated
	r, _ := fakeRepairer(t, &ctr, peer)

	_, err := r.Repair([]Instance{opsInstance()}, "/data/magelo/ops/traceway/data")
	if err == nil || !strings.Contains(err.Error(), "not a slave") {
		t.Fatalf("expected slave verification error, got %v", err)
	}
}
