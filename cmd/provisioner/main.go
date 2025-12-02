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
		fmt.Fprintf(os.Stderr, "Usage: zfs-provisioner [flags] <compose-file>\n\n")
		fmt.Fprintf(os.Stderr, "Provisions ZFS datasets based on x-zfs configuration in a Docker Compose file.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *showVersion {
		fmt.Printf("zfs-provisioner %s\n", version)
		os.Exit(0)
	}

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	composePath := flag.Arg(0)

	cfg, err := config.ParseFile(composePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	p := provisioner.New(*dryRun, *verbose, os.Stdout)

	if err := p.Provision(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
