package provisioner

import (
	"fmt"
	"io"

	"github.com/tlvenn/zfs-provisioner/internal/config"
	"github.com/tlvenn/zfs-provisioner/internal/zfs"
)

// Provisioner handles the provisioning of ZFS datasets
type Provisioner struct {
	zfs     *zfs.Client
	dryRun  bool
	verbose bool
	output  io.Writer
}

// New creates a new Provisioner
func New(dryRun, verbose bool, output io.Writer) *Provisioner {
	return &Provisioner{
		zfs:     zfs.NewClient(dryRun, verbose),
		dryRun:  dryRun,
		verbose: verbose,
		output:  output,
	}
}

// Provision processes a config and provisions all datasets
func (p *Provisioner) Provision(cfg *config.Config) error {
	if p.verbose {
		fmt.Fprintf(p.output, "provisioning datasets under %s\n", cfg.Parent)
	}

	for _, dataset := range cfg.Datasets {
		if err := p.provisionDataset(dataset); err != nil {
			return fmt.Errorf("failed to provision %s: %w", dataset.Name, err)
		}
	}

	return nil
}

// provisionDataset handles a single dataset
func (p *Provisioner) provisionDataset(dataset config.Dataset) error {
	exists, err := p.zfs.DatasetExists(dataset.Name)
	if err != nil {
		return err
	}

	if exists {
		return p.updateDataset(dataset)
	}

	return p.createDataset(dataset)
}

// createDataset creates a new dataset
func (p *Provisioner) createDataset(dataset config.Dataset) error {
	if p.dryRun {
		fmt.Fprintf(p.output, "[dry-run] would create %s%s\n", dataset.Name, formatProperties(dataset.Properties))
		return nil
	}

	if err := p.zfs.CreateDataset(dataset.Name, dataset.Properties); err != nil {
		return err
	}

	fmt.Fprintf(p.output, "created %s\n", dataset.Name)
	return nil
}

// updateDataset updates an existing dataset if properties differ
func (p *Provisioner) updateDataset(dataset config.Dataset) error {
	if p.dryRun {
		// In dry-run mode, still check what would be updated
		updated, err := p.zfs.UpdateProperties(dataset.Name, dataset.Properties)
		if err != nil {
			return err
		}

		if len(updated) > 0 {
			fmt.Fprintf(p.output, "[dry-run] would update %s\n", dataset.Name)
			for _, u := range updated {
				fmt.Fprintf(p.output, "  %s\n", u)
			}
		} else if p.verbose {
			fmt.Fprintf(p.output, "[dry-run] %s unchanged\n", dataset.Name)
		}
		return nil
	}

	updated, err := p.zfs.UpdateProperties(dataset.Name, dataset.Properties)
	if err != nil {
		return err
	}

	if len(updated) > 0 {
		fmt.Fprintf(p.output, "updated %s\n", dataset.Name)
		for _, u := range updated {
			fmt.Fprintf(p.output, "  %s\n", u)
		}
	} else if p.verbose {
		fmt.Fprintf(p.output, "%s unchanged\n", dataset.Name)
	}

	return nil
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
