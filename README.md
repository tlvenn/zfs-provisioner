# ZFS Provisioner

A Docker init container that provisions ZFS datasets based on configuration in Docker Compose files. Supports both local execution and remote provisioning over HTTP for environments where containers lack direct ZFS access.

## Overview

ZFS Provisioner reads an `x-zfs` configuration block from your `docker-compose.yml` and creates/updates ZFS datasets before your services start.

**Two modes of operation:**

- **Local mode** (default) — runs ZFS commands directly, requires privileged access to `/dev/zfs`
- **Remote mode** — sends provisioning requests to an HTTP server running on a host with ZFS access, via the `ZFS_REMOTE` environment variable

Remote mode is designed for environments like Incus/LXD where Docker runs inside containers that don't have direct ZFS access, but the host manages a ZFS pool.

## Usage

### Configuration Methods

The provisioner supports two configuration methods:

1. **Environment variable** (recommended for Portainer) - via `ZFS_CONFIG`
2. **File** - via command line argument

### Method 1: Environment Variable (Recommended)

Pass the configuration directly via the `ZFS_CONFIG` environment variable. This works seamlessly with Portainer:

```yaml
services:
  zfs-provisioner:
    image: ghcr.io/tlvenn/zfs-provisioner:latest
    privileged: true
    volumes:
      - /dev/zfs:/dev/zfs
    environment:
      ZFS_CONFIG: |
        parent: "tank/docker/stacks/myapp"
        defaults:
          compression: "zstd"
        datasets:
          redis:
            quota: "5G"
          postgres:
            data:
              quota: "50G"
              recordsize: "16K"
            wal:
              quota: "10G"

  redis:
    image: redis:7
    depends_on:
      zfs-provisioner:
        condition: service_completed_successfully
    volumes:
      - /tank/docker/stacks/myapp/redis:/data

  postgres:
    image: postgres:16
    depends_on:
      zfs-provisioner:
        condition: service_completed_successfully
    volumes:
      - /tank/docker/stacks/myapp/postgres/data:/var/lib/postgresql/data
      - /tank/docker/stacks/myapp/postgres/wal:/var/lib/postgresql/wal
```

### Method 2: File

Mount the compose file and pass the path as an argument:

```yaml
services:
  zfs-provisioner:
    image: ghcr.io/tlvenn/zfs-provisioner:latest
    privileged: true
    volumes:
      - /dev/zfs:/dev/zfs
      - ./docker-compose.yml:/config/docker-compose.yml:ro
    command: ["/config/docker-compose.yml"]
```

Note: This requires the file to be accessible at the mount path, which may not work with Portainer git deployments.

### Schema

#### `x-zfs` (required)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `parent` | string | yes | Parent ZFS dataset path (e.g., `tank/docker/stacks/myapp`) |
| `defaults` | object | no | Default properties applied to all datasets |
| `datasets` | object | no | Dataset definitions |

#### Dataset Properties

| Property | Description | Mutable | Notes |
|----------|-------------|---------|-------|
| `quota` | Maximum size limit | Yes | e.g., `10G`, `500M` |
| `compression` | Compression algorithm | Yes | e.g., `zstd`, `lz4`, `off` |
| `recordsize` | Record size | Yes | e.g., `16K`, `128K` |
| `reservation` | Guaranteed space | Yes | e.g., `5G` |
| `uid` | Owner user ID | Yes | Numeric ID, e.g., `1000` |
| `gid` | Owner group ID | Yes | Numeric ID, e.g., `1000` |

Note: `compression` and `recordsize` changes only affect newly written data.

Note: `uid` and `gid` set ownership on the dataset mountpoint using `chown -R`. This is useful for ensuring containers can write to volumes.

#### Dataset Forms

**Empty form** - dataset with no custom properties (inherits defaults only):
```yaml
datasets:
  redis: {}
  cache: {}
```
Creates: `{parent}/redis` and `{parent}/cache` with default properties.

**Simple form** - single volume with properties at the dataset level:
```yaml
datasets:
  redis:
    quota: "5G"
```
Creates: `{parent}/redis`

**Nested form** - multiple volumes per service:
```yaml
datasets:
  postgres:
    data:
      quota: "50G"
    wal:
      quota: "10G"
```
Creates: `{parent}/postgres/data` and `{parent}/postgres/wal`

**With ownership** - set uid/gid for container access:
```yaml
defaults:
  uid: "1000"
  gid: "1000"
datasets:
  redis: {}           # inherits uid=1000, gid=1000
  postgres:
    data:
      quota: "50G"
      uid: "999"      # postgres user
      gid: "999"
```
Creates datasets with specified ownership (applied recursively via `chown -R`).

### Remote Mode

For environments where Docker runs inside VMs or containers without ZFS access (e.g., Incus/LXD), the provisioner can send requests to a server running on the ZFS host.

Set the `ZFS_REMOTE` environment variable. Use `auto` with `network_mode: host` to auto-detect the gateway (recommended), or specify an explicit URL:

```yaml
services:
  zfs-provisioner:
    image: ghcr.io/tlvenn/zfs-provisioner:latest
    restart: on-failure
    network_mode: host
    environment:
      ZFS_REMOTE: "auto"
      ZFS_CONFIG: |
        parent: "rpool/data/noja/ops/myapp"
        defaults:
          compression: "zstd"
        datasets:
          clickhouse:
            quota: "50G"
            recordsize: "16K"

  clickhouse:
    depends_on:
      zfs-provisioner:
        condition: service_completed_successfully
    volumes:
      - /data/myapp/clickhouse:/var/lib/clickhouse/
```

`ZFS_REMOTE=auto` detects the default gateway from `/proc/net/route` and connects to port 9274. This works with `network_mode: host` where the container shares the host's network stack, making the gateway the machine running the ZFS server.

You can also specify an explicit URL: `ZFS_REMOTE: "http://172.16.2.1:9274"`.

Note: no `privileged: true` or `/dev/zfs` mount is needed in remote mode. The client retries with exponential backoff (1s, 2s, 4s... up to 30s cap) for 2 minutes if the server is unreachable, handling boot-order race conditions.

### CLI Options

```
Usage: zfs-provisioner [flags] [compose-file]
       zfs-provisioner serve [--listen addr]

Configuration can be provided via:
  - ZFS_CONFIG environment variable (x-zfs content as YAML)
  - ZFS_REMOTE environment variable (remote server URL)
  - File path as argument

Flags:
  --dry-run    Show what would be created/updated without making changes
  -v           Verbose output
  --version    Show version

Serve flags:
  --listen     Comma-separated addresses to listen on (default: 127.0.0.1:9274)
```

### Output

Default output (quiet mode):
```
created tank/docker/stacks/myapp/redis
created tank/docker/stacks/myapp/postgres/data
created tank/docker/stacks/myapp/postgres/wal
updated tank/docker/stacks/myapp/app/data
  quota: 50G -> 100G
```

Dry-run mode:
```
[dry-run] would create tank/docker/stacks/myapp/redis (quota=5G, compression=zstd)
[dry-run] would create tank/docker/stacks/myapp/postgres/data (quota=50G, recordsize=16K, compression=zstd)
```

## Server Mode

The `serve` subcommand runs an HTTP API that accepts provisioning requests and executes ZFS commands locally. Use this on a host that has ZFS but serves containers that don't.

### API

**`POST /provision`** — provision datasets

```json
{
  "parent": "rpool/data/noja/ops/myapp",
  "defaults": {"compression": "zstd"},
  "datasets": {
    "redis": {"quota": "5G"},
    "postgres": {
      "data": {"quota": "50G", "recordsize": "16K"},
      "wal": {"quota": "10G"}
    }
  }
}
```

Response:
```json
{
  "results": [
    {"name": "rpool/data/noja/ops/myapp/redis", "action": "created"},
    {"name": "rpool/data/noja/ops/myapp/postgres/data", "action": "created"},
    {"name": "rpool/data/noja/ops/myapp/postgres/wal", "action": "unchanged"}
  ]
}
```

Actions: `created`, `updated`, `unchanged`, `error`. Errors include an `error` field with details.

**`GET /health`** — check server and ZFS availability

Returns `{"status": "ok"}` (200) or `{"status": "error", "error": "..."}` (503).

Concurrent requests are serialized with a mutex.

### Installing the Server

Run the install script on the ZFS host:

```bash
# Download and run (uses latest GitHub release)
curl -fsSL https://raw.githubusercontent.com/tlvenn/zfs_provisioner/main/install-server.sh | sudo bash

# Or install a specific version
./install-server.sh v1.0.0

# Custom listen address
LISTEN_ADDR=0.0.0.0:9274 ./install-server.sh
```

The script:
1. **Auto-detects Incus bridges**: binds to each managed bridge IP specifically (e.g., `172.16.2.1:9274,172.16.3.1:9274`)
2. Falls back to `127.0.0.1:9274` if no Incus bridges are found
3. Downloads the `zfs-provisioner-linux-amd64` binary from GitHub releases
4. Installs it to `/usr/local/bin/`
5. Creates a hardened systemd service (`zfs-provisioner.service`)
6. Enables and starts the service
7. Runs a health check

Override auto-detection with `LISTEN_ADDR` env var or `--listen` flag.

To update, run the script again — it stops the service, replaces the binary, and restarts.

### Manual Server Setup

```bash
# Build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags='-s -w' -o zfs-provisioner ./cmd/provisioner

# Copy to host
scp zfs-provisioner myhost:/usr/local/bin/

# Start (single or multiple bridge IPs)
zfs-provisioner serve --listen 172.16.2.1:9274
zfs-provisioner serve --listen 172.16.2.1:9274,172.16.3.1:9274
```

## Building

### Local Build

```bash
go build -o zfs-provisioner ./cmd/provisioner
```

### Docker Build

```bash
docker build -t zfs-provisioner:latest .
```

## Requirements

**Local mode:**
- ZFS on Linux (host must have ZFS installed and pools configured)
- Docker with privileged container support
- Access to `/dev/zfs` device

**Remote mode:**
- ZFS provisioner server running on the ZFS host
- Network connectivity from containers to the server

## How It Works

### Local Mode

1. Provisioner container starts first (privileged, with access to `/dev/zfs`)
2. Parses `x-zfs` configuration from the compose file
3. Creates missing datasets with specified properties
4. Updates existing datasets if properties differ
5. Exits successfully (or fails if there's an error)
6. Other services start via `depends_on: condition: service_completed_successfully`

### Remote Mode

1. Provisioner container starts as an init container (no privileges needed)
2. Parses `x-zfs` configuration from `ZFS_CONFIG`
3. Sends a JSON request to the server at `ZFS_REMOTE`
4. Server creates/updates ZFS datasets and returns results
5. Client reports results and exits 0 (success) or 1 (any dataset failed)
6. Other services start via `depends_on: condition: service_completed_successfully`

## Idempotency

The provisioner is idempotent:
- Existing datasets are not recreated
- Properties are only updated if they differ from the spec
- Multiple runs produce the same result

## License

MIT
