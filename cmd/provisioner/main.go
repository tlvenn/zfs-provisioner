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
	"github.com/tlvenn/zfs-provisioner/internal/idmap"
	"github.com/tlvenn/zfs-provisioner/internal/mountcheck"
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
	allowParent := fs.String("allow-parent", "", "Comma-separated datasets under which provisioning is allowed (default: anywhere)")
	idmapRepair := fs.Bool("idmap-repair", true, "Re-idmap provisioned datasets inside running Incus containers (only when incus is installed)")
	fs.Parse(args)

	addrs := strings.Split(*listen, ",")

	// The server performs the zfs mounts itself, so it must share the host's
	// mount namespace for them to be visible anywhere else.
	if err := mountcheck.VerifyHostMountNamespace(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var opts []server.Option
	if *allowParent != "" {
		opts = append(opts, server.WithAllowedParents(strings.Split(*allowParent, ",")))
	} else {
		fmt.Fprintf(os.Stderr, "warning: no --allow-parent set: any client that can reach this server can provision or change any dataset\n")
	}
	if *idmapRepair && idmap.Available() {
		opts = append(opts, server.WithIdmapRepair(idmap.NewRepairer()))
		fmt.Fprintf(os.Stdout, "incus found: provisioned datasets will be idmapped inside running containers\n")
	}

	backend := zfs.NewClient(false)
	srv := server.New(backend, opts...)

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
		fmt.Fprintf(os.Stderr, "       zfs-provisioner serve [--listen addr[,addr...]] [--allow-parent dataset[,dataset...]] [--idmap-repair=false]\n\n")
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
		if remoteURL == "auto" {
			gw, err := client.DetectGateway()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: failed to auto-detect gateway: %v\n", err)
				os.Exit(1)
			}
			remoteURL = "http://" + gw + ":9274"
			fmt.Fprintf(os.Stdout, "detected gateway: %s\n", remoteURL)
		}
		if err := client.NewClient(remoteURL).Provision(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Local mode: run ZFS commands directly
	backend := zfs.NewClient(*dryRun)

	// In local mode this process performs the zfs mounts itself, so when it
	// runs inside a container each parent's mountpoint must have shared
	// propagation for those mounts to reach the host.
	for _, parent := range cfg.ParentNames() {
		mp, err := backend.NearestMountpoint(parent)
		if err != nil || !strings.HasPrefix(mp, "/") {
			continue
		}
		if err := mountcheck.VerifyPropagation(mp); err != nil {
			if *dryRun {
				fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
	}

	p := provisioner.New(backend, *dryRun, *verbose, os.Stdout)
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
