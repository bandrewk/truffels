# Troubleshooting

## Memory cgroups not active

Docker requires memory cgroups. If the installer fails with "Memory cgroups not active":

```bash
# Check current state
cat /sys/fs/cgroup/cgroup.controllers
# Should include: cpuset cpu io memory pids

# Fix: add to kernel command line
sudo nano /boot/firmware/cmdline.txt
# Append: cgroup_enable=memory (same line, space-separated)
sudo reboot
```

## NVMe / PCIe issues

- Use **PCIe Gen 2** (the default). Gen 3 can be unstable on Pi 5 with some NVMe drives.
- The Geekworm X1001 may need additional 5V/GND power for SSD stability.
- Check for errors: `sudo dmesg -T | grep -iE 'nvme|pcie|aer|timeout|reset'`

## Container health checks

```bash
# Check all container states
sudo docker ps --format "table {{.Names}}\t{{.Status}}"

# Check a specific container's logs
sudo docker logs truffels-bitcoind --tail 50

# Check compose stack
cd /srv/truffels/compose/<service> && sudo docker compose ps
```

## Mempool shows "Error loading data"

All mempool traffic must route through the **mempool frontend** container, not directly to the backend. The frontend nginx rewrites `/api/block/...` to `/api/v1/block/...`. If the reverse proxy sends API requests directly to the backend, block/tx detail pages will 404.

## Mempool backend OOM crashes

If mempool backend repeatedly crashes with "JavaScript heap out of memory", add to the compose environment:

```yaml
environment:
  NODE_OPTIONS: "--max-old-space-size=768"
```

## ckstats blank page

ckstats requires `basePath: '/ckstats'` in `next.config.js`. Without this, assets load from the wrong path. The Caddy proxy must **not** use `uri strip_prefix /ckstats` — Next.js handles the prefix internally.

## electrs index incompatibility

If migrating from a source-compiled electrs to the Docker image, the RocksDB index format may differ. Delete the old index and let it rebuild:

```bash
sudo rm -rf /srv/truffels/data/electrs/db
cd /srv/truffels/compose/electrs && sudo docker compose restart
```

Full reindex takes several hours.

## Bitcoin Core config

Bitcoin Core reads `<datadir>/bitcoin.conf` by default. The installer uses an explicit `-conf=` flag to point to `/bitcoin.conf` inside the container. The `rpcauth` format uses a colon separator: `user:salt$hash`.

## Database containers won't start

MariaDB and PostgreSQL need specific Linux capabilities for their entrypoint scripts (user switching):

```yaml
cap_add:
  - CHOWN
  - DAC_OVERRIDE
  - FOWNER
  - SETGID
  - SETUID
```

`no-new-privileges: true` also breaks database entrypoints — don't set it for DB containers.

## Permission errors

The truffels-api runs as `user: "1000:1000"` with `cap_drop: ALL`. This means it has no `DAC_OVERRIDE` — it can only access files owned by uid 1000. If you see permission errors:

```bash
sudo chown -R 1000:1000 /srv/truffels/data/truffels
sudo chgrp -R 1000 /srv/truffels/secrets
sudo chmod 750 /srv/truffels/secrets
sudo chmod 640 /srv/truffels/secrets/*
```

## Firewall

The installer sets up nftables with a default DROP policy on INPUT. Allowed ports: 22 (SSH), 80 (HTTP), 3333 (stratum), 8333 (Bitcoin P2P), plus loopback and Docker bridge traffic.

To check firewall rules:

```bash
sudo nft list ruleset
```

## Rebuilding control plane containers

After modifying source code for truffels-api, truffels-web, or truffels-agent:

```bash
cd /srv/truffels/compose/truffels
sudo docker compose build --no-cache <api|web|agent>
sudo docker compose up -d <api|web|agent>
```

**Do not** run build tools (Go, Node, pnpm) directly on the host — all builds happen inside Docker.

## Logs

```bash
# Container logs
sudo docker logs <container-name> --tail 100

# System journal
journalctl -u docker --no-pager -n 50

# All container logs for a compose stack
cd /srv/truffels/compose/<service> && sudo docker compose logs --tail 50
```
