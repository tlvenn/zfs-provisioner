# ZFS Provisioner

A Docker init container that provisions ZFS datasets based on configuration in Docker Compose files. Designed to work with Portainer for declarative storage management.

## Overview

ZFS Provisioner reads an `x-zfs` configuration block from your `docker-compose.yml` and creates/updates ZFS datasets before your services start. This enables declarative storage provisioning while using Portainer's UI for deployments.

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

Note: `compression` and `recordsize` changes only affect newly written data.

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

### CLI Options

```
Usage: zfs-provisioner [flags] [compose-file]

Configuration can be provided via:
  - ZFS_CONFIG environment variable (x-zfs content as YAML)
  - File path as argument

Flags:
  --dry-run    Show what would be created/updated without making changes
  -v           Verbose output
  --version    Show version
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

- ZFS on Linux (host must have ZFS installed and pools configured)
- Docker with privileged container support
- Access to `/dev/zfs` device

## How It Works

1. Provisioner container starts first (privileged, with access to `/dev/zfs`)
2. Parses `x-zfs` configuration from the compose file
3. Creates missing datasets with specified properties
4. Updates existing datasets if properties differ
5. Exits successfully (or fails if there's an error)
6. Other services start via `depends_on: condition: service_completed_successfully`

## Idempotency

The provisioner is idempotent:
- Existing datasets are not recreated
- Properties are only updated if they differ from the spec
- Multiple runs produce the same result

## License

MIT
