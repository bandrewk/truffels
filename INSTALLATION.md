# Installation

## Prerequisites

- Raspberry Pi 5 (8GB recommended)
- Raspberry Pi OS Lite 64-bit (fresh install)
- NVMe storage (recommended) or SD card
- Internet connection

## Steps

### 1. Update the system

```bash
sudo apt update
sudo apt full-upgrade -y
sudo apt autoremove -y
```

### 2. Enable memory cgroups

Docker requires memory cgroups. Add `cgroup_enable=memory` to the kernel command line:

```bash
sudo nano /boot/firmware/cmdline.txt
```

Append `cgroup_enable=memory` to the **same line** (space-separated, do not add a new line). Then reboot:

```bash
sudo reboot -h now
```

### 3. Install git

```bash
sudo apt install -y git
```

### 4. Clone and run the installer

```bash
git clone https://github.com/bandrewk/truffels.git
cd truffels
sudo ./install.sh
```

The installer will:
- Install Docker Engine and Compose plugin
- Create the directory layout under `/srv/truffels/`
- Generate RPC and database credentials
- Prompt for pruning mode (full node or pruned)
- Prompt for a Bitcoin mining address (for solo mining via ckpool)
- Pull/build all container images
- Start all services
- Set up the firewall and swap

### Non-interactive install

Skip prompts by setting environment variables:

```bash
sudo TRUFFELS_PRUNE_SIZE=0 \
     TRUFFELS_MINING_ADDRESS=<your-btc-address> \
     ./install.sh
```

| Variable | Default | Description |
|----------|---------|-------------|
| `TRUFFELS_PRUNE_SIZE` | interactive | `0` = full node, `550`+ = pruned (MiB to keep) |
| `TRUFFELS_MINING_ADDRESS` | interactive | Bitcoin address for solo mining rewards |
| `TRUFFELS_MINING_SIG` | `/truffels/` | Coinbase signature for mined blocks |
| `TRUFFELS_VERSION` | `v0.2.0` | Version tag for control plane images |

### Installer flags

```bash
sudo ./install.sh --skip-docker      # Skip Docker installation (already installed)
sudo ./install.sh --skip-pull        # Skip pulling pre-built images
sudo ./install.sh --restore-from /path/to/backup  # Restore data from backup
```

## After installation

- **Web UI:** `http://<your-pi-ip>/admin`
- **Block explorer (mempool):** `http://<your-pi-ip>/`
- **Mining stats:** `http://<your-pi-ip>/ckstats/`
- **Stratum (miners):** `<your-pi-ip>:3333`
- **Electrum (wallets):** `<your-pi-ip>:50001` (TCP, no SSL)

On first install, all dependent services (electrs, mempool, ckpool, ckstats) are **disabled** until Bitcoin Core has fully synced. Enable them via the web UI once sync is complete.

**Pruned mode:** electrs and mempool are incompatible with pruning and cannot be enabled. ckpool and ckstats work fine with pruned nodes.

## SD card notes

The installer auto-detects SD card boot and adapts:
- Skips NVMe swap file (preserves SD write endurance)
- Auto-sets pruning if storage is under 500GB
- Warns about performance and write wear

NVMe is strongly recommended for full nodes and production use.

## Ports

| Port | Service | Exposure |
|------|---------|----------|
| 22 | SSH | LAN |
| 80 | Caddy (web UI + block explorer) | LAN |
| 3333 | ckpool stratum | LAN |
| 8333 | Bitcoin P2P | LAN |
| 50001 | electrs Electrum protocol | LAN |

No services are exposed to the internet by default.

## Updating

The web UI checks for updates automatically (configurable interval, default 24h). Updates can be applied one-click from the Updates page with automatic rollback on health check failure.

## Troubleshooting

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for platform quirks, known issues, and debugging tips.
