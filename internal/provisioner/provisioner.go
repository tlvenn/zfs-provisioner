package provisioner

import (
	"fmt"
	"io"

	"github.com/tlvenn/zfs-provisioner/internal/config"
)

// Backend defines the interface for ZFS dataset operations
type Backend interface {
	DatasetExists(name string) (bool, error)
	CreateDataset(name string, props config.ZFSProperties) error
	UpdateProperties(name string, desired config.ZFSProperties) ([]string, error)
}

// Provisioner handles the provisioning of ZFS datasets
type Provisioner struct {
	zfs     Backend
	dryRun  bool
	verbose bool
	output  io.Writer
}

// New creates a new Provisioner
func New(backend Backend, dryRun, verbose bool, output io.Writer) *Provisioner {
	return &Provisioner{
		zfs:     backend,
		dryRun:  dryRun,
		verbose: verbose,
		output:  output,
	}
}

// Provision processes a config and provisions all datasets.
// Returns an error on the first dataset that fails.
func (p *Provisioner) Provision(cfg *config.Config) error {
	if p.verbose {
		fmt.Fprintf(p.output, "provisioning datasets under %s\n", cfg.Parent)
	}

	results := p.ProvisionWithResults(cfg)
	for _, r := range results {
		if r.Action == "error" {
			return fmt.Errorf("failed to provision %s: %s", r.Name, r.Error)
		}
	}

	return nil
}

// ProvisionWithResults processes a config and returns structured results per dataset
func (p *Provisioner) ProvisionWithResults(cfg *config.Config) []config.DatasetResult {
	var results []config.DatasetResult

	for _, dataset := range cfg.Datasets {
		results = append(results, p.provisionDataset(dataset))
	}

	return results
}

func (p *Provisioner) provisionDataset(dataset config.Dataset) config.DatasetResult {
	exists, err := p.zfs.DatasetExists(dataset.Name)
	if err != nil {
		return config.DatasetResult{Name: dataset.Name, Action: "error", Error: err.Error()}
	}

	if !exists {
		return p.createDataset(dataset)
	}

	return p.updateDataset(dataset)
}

func (p *Provisioner) createDataset(dataset config.Dataset) config.DatasetResult {
	if p.dryRun {
		fmt.Fprintf(p.output, "[dry-run] would create %s%s\n", dataset.Name, formatProperties(dataset.Properties))
		return config.DatasetResult{Name: dataset.Name, Action: "created"}
	}

	if err := p.zfs.CreateDataset(dataset.Name, dataset.Properties); err != nil {
		return config.DatasetResult{Name: dataset.Name, Action: "error", Error: err.Error()}
	}

	fmt.Fprintf(p.output, "created %s\n", dataset.Name)
	return config.DatasetResult{Name: dataset.Name, Action: "created"}
}

func (p *Provisioner) updateDataset(dataset config.Dataset) config.DatasetResult {
	changes, err := p.zfs.UpdateProperties(dataset.Name, dataset.Properties)
	if err != nil {
		return config.DatasetResult{Name: dataset.Name, Action: "error", Error: err.Error()}
	}

	if len(changes) > 0 {
		if p.dryRun {
			fmt.Fprintf(p.output, "[dry-run] would update %s\n", dataset.Name)
		} else {
			fmt.Fprintf(p.output, "updated %s\n", dataset.Name)
		}
		for _, c := range changes {
			fmt.Fprintf(p.output, "  %s\n", c)
		}
		return config.DatasetResult{Name: dataset.Name, Action: "updated", Changes: changes}
	}

	if p.verbose {
		if p.dryRun {
			fmt.Fprintf(p.output, "[dry-run] %s unchanged\n", dataset.Name)
		} else {
			fmt.Fprintf(p.output, "%s unchanged\n", dataset.Name)
		}
	}

	return config.DatasetResult{Name: dataset.Name, Action: "unchanged"}
}

// formatProperties formats properties for display
func formatProperties(props config.ZFSProperties) string {
	var parts []string

	if props.Quota != "" {
		parts = append(parts, "quota="+props.Quota)
	}
	if props.Compression != "" {
		parts = append(parts, "compression="+props.Compression)
	}
	if props.Recordsize != "" {
		parts = append(parts, "recordsize="+props.Recordsize)
	}
	if props.Reservation != "" {
		parts = append(parts, "reservation="+props.Reservation)
	}
	if props.UID != "" {
		parts = append(parts, "uid="+props.UID)
	}
	if props.GID != "" {
		parts = append(parts, "gid="+props.GID)
	}

	if len(parts) == 0 {
		return ""
	}

	result := " ("
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	result += ")"

	return result
}
