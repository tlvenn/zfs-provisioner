package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tlvenn/zfs-provisioner/internal/client"
	"github.com/tlvenn/zfs-provisioner/internal/config"
	"github.com/tlvenn/zfs-provisioner/internal/provisioner"
	"github.com/tlvenn/zfs-provisioner/internal/server"
	"github.com/tlvenn/zfs-provisioner/internal/zfs"
)

var (
	version = "dev"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		runServe(os.Args[2:])
		return
	}

	runProvision(os.Args[1:])
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:9274", "Comma-separated addresses to listen on")
	fs.Parse(args)

	addrs := strings.Split(*listen, ",")

	backend := zfs.NewClient(false)
	srv := server.New(backend)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.ListenAndServe(ctx, addrs); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runProvision(args []string) {
	fs := flag.NewFlagSet("provision", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Show what would be created/updated without making changes")
	verbose := fs.Bool("v", false, "Verbose output")
	showVersion := fs.Bool("version", false, "Show version")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: zfs-provisioner [flags] [compose-file]\n")
		fmt.Fprintf(os.Stderr, "       zfs-provisioner serve [--listen addr[,addr...]]\n\n")
		fmt.Fprintf(os.Stderr, "Provisions ZFS datasets based on x-zfs configuration.\n\n")
		fmt.Fprintf(os.Stderr, "Configuration can be provided via:\n")
		fmt.Fprintf(os.Stderr, "  - File path as argument\n")
		fmt.Fprintf(os.Stderr, "  - ZFS_CONFIG environment variable (x-zfs content as YAML)\n")
		fmt.Fprintf(os.Stderr, "  - ZFS_REMOTE environment variable (remote server URL)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)

	if *showVersion {
		fmt.Printf("zfs-provisioner %s\n", version)
		os.Exit(0)
	}

	cfg, err := parseConfig(fs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Remote mode: send config to server
	if remoteURL := os.Getenv("ZFS_REMOTE"); remoteURL != "" {
		if err := client.NewClient(remoteURL).Provision(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Local mode: run ZFS commands directly
	p := provisioner.New(zfs.NewClient(*dryRun), *dryRun, *verbose, os.Stdout)
	if err := p.Provision(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func parseConfig(fs *flag.FlagSet) (*config.Config, error) {
	if envConfig := os.Getenv("ZFS_CONFIG"); envConfig != "" {
		return config.ParseEnv(envConfig)
	}

	if fs.NArg() == 1 {
		return config.ParseFile(fs.Arg(0))
	}

	return nil, fmt.Errorf("no configuration provided\nProvide either ZFS_CONFIG environment variable or a compose file path")
}
