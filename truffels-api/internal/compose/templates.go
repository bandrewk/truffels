package compose

import (
	"bytes"
	"fmt"
	"text/template"
)

// Compose templates — source of truth for all managed service compose files.
// Image tags are parameterized; everything else (memory, caps, healthchecks) is hardcoded.

var templates map[string]*template.Template

func init() {
	templates = make(map[string]*template.Template, len(rawTemplates))
	for id, raw := range rawTemplates {
		t, err := template.New(id).Parse(raw)
		if err != nil {
			panic(fmt.Sprintf("compose: bad template %q: %v", id, err))
		}
		templates[id] = t
	}
}

// Render produces the compose file content for the given service with the supplied params.
func Render(serviceID string, params any) (string, error) {
	t, ok := templates[serviceID]
	if !ok {
		return "", fmt.Errorf("compose: no template for %q", serviceID)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("compose: render %q: %w", serviceID, err)
	}
	return buf.String(), nil
}

var rawTemplates = map[string]string{
	"bitcoind": bitcoinTemplate,
	"electrs":  electrsTemplate,
	"ckpool":   ckpoolTemplate,
	"mempool":  mempoolTemplate,
	"ckstats":  ckstatsTemplate,
	"proxy":    proxyTemplate,
}

const bitcoinTemplate = `# Project Truffels — Bitcoin Core
# Managed by truffels. Do not edit manually.

services:
  bitcoind:
    image: {{.ImageTag}}
    container_name: truffels-bitcoind
    restart: unless-stopped
    user: "1000:1000"
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    ports:
      - "8333:8333"   # P2P — needed for inbound peer connections
    volumes:
      - /srv/truffels/data/bitcoin/blockchain:/data
      - /srv/truffels/config/bitcoin/bitcoin.conf:/bitcoin.conf:ro
    entrypoint: ["bitcoind", "-conf=/bitcoin.conf", "-datadir=/data", "-printtoconsole"]
    stop_grace_period: 120s
    deploy:
      resources:
        limits:
          memory: 3500M
    healthcheck:
      test: ["CMD", "bitcoin-cli", "-conf=/bitcoin.conf", "-datadir=/data", "-getinfo"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 60s

networks:
  bitcoin-backend:
    external: true
`

const electrsTemplate = `# Project Truffels — electrs (Electrum Server)
# Managed by truffels. Do not edit manually.

services:
  electrs:
    image: {{.ImageTag}}
    container_name: truffels-electrs
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    ports:
      - "50001:50001"   # Electrum protocol — LAN wallet connectivity
    networks:
      bitcoin-backend:
    volumes:
      - /srv/truffels/data/electrs:/data
      - /srv/truffels/config/electrs/electrs.toml:/etc/electrs/electrs.toml:ro
    entrypoint: ["electrs", "--conf", "/etc/electrs/electrs.toml"]
    stop_grace_period: 30s
    deploy:
      resources:
        limits:
          memory: 2048M
    healthcheck:
      test: ["CMD-SHELL", "pidof electrs || exit 1"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 120s

networks:
  bitcoin-backend:
    external: true
`

const ckpoolTemplate = `# Project Truffels — ckpool (Solo Mining Pool)
# Managed by truffels. Do not edit manually.

services:
  ckpool:
    image: {{.ImageTag}}
    container_name: truffels-ckpool
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    ports:
      - "3333:3333"   # Stratum — LAN access for miners
    volumes:
      - /srv/truffels/data/ckpool:/data
      - /srv/truffels/config/ckpool/ckpool.conf:/etc/ckpool/ckpool.conf:ro
    entrypoint: ["sh", "-c", "rm -f /tmp/ckpool/main.pid; exec ckpool -B -l 4 -c /etc/ckpool/ckpool.conf"]
    stop_grace_period: 10s
    deploy:
      resources:
        limits:
          memory: 512M
    healthcheck:
      test: ["CMD-SHELL", "pidof ckpool || exit 1"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 30s

networks:
  bitcoin-backend:
    external: true
`

const mempoolTemplate = `# Project Truffels — mempool.space (Block Explorer)
# Managed by truffels. Do not edit manually.

services:
  mempool-backend:
    image: {{.BackendImageTag}}
    container_name: truffels-mempool-backend
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/secrets/mempool-backend.env
    environment:
      NODE_OPTIONS: "--max-old-space-size=768"
      MEMPOOL_BACKEND: "electrum"
      ELECTRUM_HOST: "truffels-electrs"
      ELECTRUM_PORT: "50001"
      ELECTRUM_TLS_ENABLED: "false"
      CORE_RPC_HOST: "truffels-bitcoind"
      CORE_RPC_PORT: "8332"
      DATABASE_ENABLED: "true"
      DATABASE_HOST: "truffels-mempool-db"
      DATABASE_PORT: "3306"
      DATABASE_DATABASE: "mempool"
      STATISTICS_ENABLED: "true"
    depends_on:
      mempool-db:
        condition: service_healthy
    deploy:
      resources:
        limits:
          memory: 1024M

  mempool-frontend:
    image: {{.FrontendImageTag}}
    container_name: truffels-mempool-frontend
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    environment:
      FRONTEND_HTTP_PORT: "8080"
      BACKEND_MAINNET_HTTP_HOST: "truffels-mempool-backend"
    depends_on:
      - mempool-backend
    deploy:
      resources:
        limits:
          memory: 256M

  mempool-db:
    image: {{.DBImageTag}}
    container_name: truffels-mempool-db
    restart: unless-stopped
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - SETGID
      - SETUID
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/secrets/mempool-db.env
    volumes:
      - /srv/truffels/data/mempool/mysql:/var/lib/mysql
    deploy:
      resources:
        limits:
          memory: 512M
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 30s

networks:
  bitcoin-backend:
    external: true
`

const ckstatsTemplate = `# Project Truffels — ckstats (Mining Stats Dashboard)
# Managed by truffels. Do not edit manually.

services:
  ckstats:
    build:
      context: /srv/truffels/data
      dockerfile: /srv/truffels/compose/ckstats/Dockerfile
    image: {{.CkstatsImageTag}}
    container_name: truffels-ckstats
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/config/ckstats/.env
    volumes:
      - /srv/truffels/data/ckpool/logs:/ckpool-logs:ro
    depends_on:
      ckstats-db:
        condition: service_healthy
    deploy:
      resources:
        limits:
          memory: 512M
    healthcheck:
      test: ["CMD-SHELL", "node -e 'fetch(\"http://127.0.0.1:3000/ckstats\").then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))'"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 30s

  ckstats-cron:
    image: {{.CkstatsImageTag}}
    container_name: truffels-ckstats-cron
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/config/ckstats/.env
    volumes:
      - /srv/truffels/data/ckpool/logs:/ckpool-logs:ro
    depends_on:
      ckstats:
        condition: service_healthy
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        CLEANUP_COUNTER=0
        while true; do
          echo "[$(date)] Running seed..."
          pnpm seed 2>&1
          echo "[$(date)] Running update-users..."
          pnpm update-users 2>&1
          CLEANUP_COUNTER=$((CLEANUP_COUNTER + 1))
          if [ "$$CLEANUP_COUNTER" -ge 120 ]; then
            echo "[$(date)] Running cleanup..."
            pnpm cleanup 2>&1
            CLEANUP_COUNTER=0
          fi
          sleep 60
        done
    deploy:
      resources:
        limits:
          memory: 256M

  ckstats-db:
    image: {{.DBImageTag}}
    container_name: truffels-ckstats-db
    restart: unless-stopped
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - SETGID
      - SETUID
    networks:
      bitcoin-backend:
    env_file:
      - /srv/truffels/secrets/ckstats-db.env
    volumes:
      - /srv/truffels/data/ckstats/postgres:/var/lib/postgresql/data
    deploy:
      resources:
        limits:
          memory: 256M
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ckpool -d ckstats"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 15s

networks:
  bitcoin-backend:
    external: true
`

const proxyTemplate = `# Project Truffels — Reverse Proxy (Caddy)
# Managed by truffels. Do not edit manually.

services:
  proxy:
    image: {{.ImageTag}}
    container_name: truffels-proxy
    restart: unless-stopped
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - DAC_OVERRIDE
      - FOWNER
      - NET_BIND_SERVICE
      - SETGID
      - SETUID
    networks:
      truffels-edge:
      bitcoin-backend:
    ports:
      - "80:80"
    volumes:
      - /srv/truffels/config/proxy/Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    deploy:
      resources:
        limits:
          memory: 128M
    healthcheck:
      test: ["CMD", "wget", "--spider", "--quiet", "http://127.0.0.1:80/"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s

volumes:
  caddy_data:
  caddy_config:

networks:
  truffels-edge:
    external: true
  bitcoin-backend:
    external: true
`
