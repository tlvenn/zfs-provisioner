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

	// Set ownership if uid or gid is specified
	if props.UID != "" || props.GID != "" {
		mountpoint, err := c.GetMountpoint(name)
		if err != nil {
			return err
		}
		if err := c.SetOwnership(mountpoint, props.UID, props.GID); err != nil {
			return err
		}
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

	// Check and update ownership if uid or gid is specified
	if desired.UID != "" || desired.GID != "" {
		mountpoint, err := c.GetMountpoint(name)
		if err != nil {
			return updated, err
		}

		currentUID, currentGID, err := c.GetOwnership(mountpoint)
		if err != nil {
			return updated, err
		}

		needsUpdate := false
		if desired.UID != "" && desired.UID != currentUID {
			needsUpdate = true
		}
		if desired.GID != "" && desired.GID != currentGID {
			needsUpdate = true
		}

		if needsUpdate {
			if !c.dryRun {
				if err := c.SetOwnership(mountpoint, desired.UID, desired.GID); err != nil {
					return updated, err
				}
			}

			// Build update message
			oldOwnership := currentUID + ":" + currentGID
			newUID := desired.UID
			if newUID == "" {
				newUID = currentUID
			}
			newGID := desired.GID
			if newGID == "" {
				newGID = currentGID
			}
			newOwnership := newUID + ":" + newGID
			updated = append(updated, fmt.Sprintf("ownership: %s -> %s", oldOwnership, newOwnership))
		}
	}

	return updated, nil
}

// normalizeSize normalizes size strings for comparison (e.g., "1G" vs "1073741824")
func normalizeSize(s string) string {
	// For now, just lowercase and trim - could expand to handle byte conversion
	return strings.ToLower(strings.TrimSpace(s))
}

// GetMountpoint returns the mountpoint path for a dataset
func (c *Client) GetMountpoint(name string) (string, error) {
	cmd := exec.Command("zfs", "get", "-H", "-o", "value", "mountpoint", name)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to get mountpoint: %s", stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// GetOwnership returns the current uid and gid of a mountpoint
func (c *Client) GetOwnership(mountpoint string) (uid, gid string, err error) {
	// Use stat to get ownership - format differs by OS
	// On Linux: stat -c '%u:%g' <path>
	// On macOS/BSD: stat -f '%u:%g' <path>
	var cmd *exec.Cmd

	// Try Linux format first, fall back to BSD format
	cmd = exec.Command("stat", "-c", "%u:%g", mountpoint)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Try BSD/macOS format
		cmd = exec.Command("stat", "-f", "%u:%g", mountpoint)
		stdout.Reset()
		stderr.Reset()
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return "", "", fmt.Errorf("failed to get ownership: %s", stderr.String())
		}
	}

	parts := strings.Split(strings.TrimSpace(stdout.String()), ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected stat output: %s", stdout.String())
	}

	return parts[0], parts[1], nil
}

// SetOwnership sets the uid:gid ownership on a mountpoint recursively
func (c *Client) SetOwnership(mountpoint, uid, gid string) error {
	if c.dryRun {
		return nil
	}

	var ownership string
	if uid != "" && gid != "" {
		ownership = uid + ":" + gid
	} else if uid != "" {
		ownership = uid
	} else if gid != "" {
		ownership = ":" + gid
	} else {
		// Nothing to set
		return nil
	}

	cmd := exec.Command("chown", "-R", ownership, mountpoint)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set ownership: %s", stderr.String())
	}

	return nil
}
