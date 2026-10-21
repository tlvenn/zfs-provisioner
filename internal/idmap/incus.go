package idmap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ListInstances returns the running containers on this host, across all
// Incus projects.
func ListInstances() ([]Instance, error) {
	cmd := exec.Command("incus", "query", "/1.0/instances?all-projects=true&recursion=2")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("incus query failed: %s", strings.TrimSpace(stderr.String()))
	}

	return parseInstances(stdout.Bytes())
}

func parseInstances(data []byte) ([]Instance, error) {
	var raw []struct {
		Name            string                       `json:"name"`
		Project         string                       `json:"project"`
		Type            string                       `json:"type"`
		Status          string                       `json:"status"`
		ExpandedDevices map[string]map[string]string `json:"expanded_devices"`
		State           *struct {
			Pid int `json:"pid"`
		} `json:"state"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing incus instances: %w", err)
	}

	var instances []Instance
	for _, r := range raw {
		if r.Type != "container" || r.Status != "Running" || r.State == nil || r.State.Pid <= 0 {
			continue
		}
		instances = append(instances, Instance{
			Project: r.Project,
			Name:    r.Name,
			Pid:     r.State.Pid,
			Devices: r.ExpandedDevices,
		})
	}

	return instances, nil
}
