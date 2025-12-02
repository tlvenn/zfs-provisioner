package zfs

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tlvenn/zfs-provisioner/internal/config"
)

// Client handles ZFS operations
type Client struct {
	dryRun  bool
	verbose bool
}

// NewClient creates a new ZFS client
func NewClient(dryRun, verbose bool) *Client {
	return &Client{
		dryRun:  dryRun,
		verbose: verbose,
	}
}

// DatasetExists checks if a dataset exists
func (c *Client) DatasetExists(name string) (bool, error) {
	cmd := exec.Command("zfs", "list", "-H", "-o", "name", name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Exit code 1 with "dataset does not exist" means it doesn't exist
		if strings.Contains(stderr.String(), "does not exist") {
			return false, nil
		}
		return false, fmt.Errorf("failed to check dataset: %s", stderr.String())
	}

	return true, nil
}

// GetProperties returns the current properties of a dataset
func (c *Client) GetProperties(name string) (config.ZFSProperties, error) {
	props := config.ZFSProperties{}

	// Get all properties we care about in one call
	cmd := exec.Command("zfs", "get", "-H", "-o", "property,value", "quota,compression,recordsize,reservation", name)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return props, fmt.Errorf("failed to get properties: %s", stderr.String())
	}

	// Parse output: each line is "property\tvalue"
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			continue
		}

		prop, value := parts[0], parts[1]
		switch prop {
		case "quota":
			if value != "none" && value != "0" {
				props.Quota = value
			}
		case "compression":
			props.Compression = value
		case "recordsize":
			props.Recordsize = value
		case "reservation":
			if value != "none" && value != "0" {
				props.Reservation = value
			}
		}
	}

	return props, nil
}

// CreateDataset creates a new ZFS dataset with the specified properties
func (c *Client) CreateDataset(name string, props config.ZFSProperties) error {
	args := []string{"create"}

	// Add properties
	if props.Quota != "" {
		args = append(args, "-o", "quota="+props.Quota)
	}
	if props.Compression != "" {
		args = append(args, "-o", "compression="+props.Compression)
	}
	if props.Recordsize != "" {
		args = append(args, "-o", "recordsize="+props.Recordsize)
	}
	if props.Reservation != "" {
		args = append(args, "-o", "reservation="+props.Reservation)
	}

	// Create parent datasets if needed
	args = append(args, "-p")
	args = append(args, name)

	if c.dryRun {
		return nil
	}

	cmd := exec.Command("zfs", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create dataset: %s", stderr.String())
	}

	return nil
}

// SetProperty sets a single property on a dataset
func (c *Client) SetProperty(name, property, value string) error {
	if c.dryRun {
		return nil
	}

	cmd := exec.Command("zfs", "set", property+"="+value, name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set %s: %s", property, stderr.String())
	}

	return nil
}

// UpdateProperties updates the properties of an existing dataset
// Returns a list of properties that were updated
func (c *Client) UpdateProperties(name string, desired config.ZFSProperties) ([]string, error) {
	current, err := c.GetProperties(name)
	if err != nil {
		return nil, err
	}

	var updated []string

	// Check and update each property
	if desired.Quota != "" && normalizeSize(desired.Quota) != normalizeSize(current.Quota) {
		if !c.dryRun {
			if err := c.SetProperty(name, "quota", desired.Quota); err != nil {
				return updated, err
			}
		}
		updated = append(updated, fmt.Sprintf("quota: %s -> %s", current.Quota, desired.Quota))
	}

	if desired.Compression != "" && desired.Compression != current.Compression {
		if !c.dryRun {
			if err := c.SetProperty(name, "compression", desired.Compression); err != nil {
				return updated, err
			}
		}
		updated = append(updated, fmt.Sprintf("compression: %s -> %s (only affects new data)", current.Compression, desired.Compression))
	}

	if desired.Recordsize != "" && normalizeSize(desired.Recordsize) != normalizeSize(current.Recordsize) {
		if !c.dryRun {
			if err := c.SetProperty(name, "recordsize", desired.Recordsize); err != nil {
				return updated, err
			}
		}
		updated = append(updated, fmt.Sprintf("recordsize: %s -> %s (only affects new files)", current.Recordsize, desired.Recordsize))
	}

	if desired.Reservation != "" && normalizeSize(desired.Reservation) != normalizeSize(current.Reservation) {
		if !c.dryRun {
			if err := c.SetProperty(name, "reservation", desired.Reservation); err != nil {
				return updated, err
			}
		}
		updated = append(updated, fmt.Sprintf("reservation: %s -> %s", current.Reservation, desired.Reservation))
	}

	return updated, nil
}

// normalizeSize normalizes size strings for comparison (e.g., "1G" vs "1073741824")
func normalizeSize(s string) string {
	// For now, just lowercase and trim - could expand to handle byte conversion
	return strings.ToLower(strings.TrimSpace(s))
}
