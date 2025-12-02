package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tlvenn/zfs-provisioner/internal/config"
	"github.com/tlvenn/zfs-provisioner/internal/provisioner"
)

var (
	version = "dev"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "Show what would be created/updated without making changes")
	verbose := flag.Bool("v", false, "Verbose output")
	showVersion := flag.Bool("version", false, "Show version")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: zfs-provisioner [flags] [compose-file]\n\n")
		fmt.Fprintf(os.Stderr, "Provisions ZFS datasets based on x-zfs configuration.\n\n")
		fmt.Fprintf(os.Stderr, "Configuration can be provided via:\n")
		fmt.Fprintf(os.Stderr, "  - File path as argument\n")
		fmt.Fprintf(os.Stderr, "  - ZFS_CONFIG environment variable (x-zfs content as YAML)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *showVersion {
		fmt.Printf("zfs-provisioner %s\n", version)
		os.Exit(0)
	}

	var cfg *config.Config
	var err error

	// Check for ZFS_CONFIG environment variable first
	if envConfig := os.Getenv("ZFS_CONFIG"); envConfig != "" {
		cfg, err = config.ParseEnv(envConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error parsing ZFS_CONFIG: %v\n", err)
			os.Exit(1)
		}
	} else if flag.NArg() == 1 {
		// Fall back to file argument
		cfg, err = config.ParseFile(flag.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintf(os.Stderr, "error: no configuration provided\n")
		fmt.Fprintf(os.Stderr, "Provide either ZFS_CONFIG environment variable or a compose file path\n")
		os.Exit(1)
	}

	p := provisioner.New(*dryRun, *verbose, os.Stdout)

	if err := p.Provision(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
