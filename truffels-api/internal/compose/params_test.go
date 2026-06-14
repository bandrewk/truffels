package compose

import "testing"

func TestExtractImageTag_Simple(t *testing.T) {
	content := `    image: btcpayserver/bitcoin:30.2`
	got := ExtractImageTag(content, "btcpayserver/bitcoin:")
	if got != "btcpayserver/bitcoin:30.2" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractImageTag_WithDigest(t *testing.T) {
	content := `    image: mariadb:lts@sha256:8164f184d16c30e2f159e30518113667b796306dff0fe558876ab1ff521a682f`
	got := ExtractImageTag(content, "mariadb:")
	if got != "mariadb:lts@sha256:8164f184d16c30e2f159e30518113667b796306dff0fe558876ab1ff521a682f" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractImageTag_LocalBuild(t *testing.T) {
	content := `    image: truffels/ckstats:latest`
	got := ExtractImageTag(content, "truffels/ckstats:")
	if got != "truffels/ckstats:latest" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractImageTag_NotFound(t *testing.T) {
	content := `    image: caddy:2.11.2-alpine`
	got := ExtractImageTag(content, "nginx:")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestExtractParams_Bitcoin(t *testing.T) {
	content := `    image: btcpayserver/bitcoin:30.2`
	p, err := ExtractParams("bitcoind", content)
	if err != nil {
		t.Fatal(err)
	}
	bp := p.(BitcoinParams)
	if bp.ImageTag != "btcpayserver/bitcoin:30.2" {
		t.Fatalf("got %q", bp.ImageTag)
	}
}

func TestExtractParams_Mempool(t *testing.T) {
	content := `
    image: mempool/backend:v3.2.1
    image: mempool/frontend:v3.2.1
    image: mariadb:lts@sha256:abc123
`
	p, err := ExtractParams("mempool", content)
	if err != nil {
		t.Fatal(err)
	}
	mp := p.(MempoolParams)
	if mp.BackendImageTag != "mempool/backend:v3.2.1" {
		t.Fatalf("backend: %q", mp.BackendImageTag)
	}
	if mp.FrontendImageTag != "mempool/frontend:v3.2.1" {
		t.Fatalf("frontend: %q", mp.FrontendImageTag)
	}
	if mp.DBImageTag != "mariadb:lts@sha256:abc123" {
		t.Fatalf("db: %q", mp.DBImageTag)
	}
}

func TestExtractParams_Ckstats(t *testing.T) {
	content := `
    image: truffels/ckstats:latest
    image: truffels/ckstats:latest
    image: postgres:16.13-alpine
`
	p, err := ExtractParams("ckstats", content)
	if err != nil {
		t.Fatal(err)
	}
	cp := p.(CkstatsParams)
	if cp.CkstatsImageTag != "truffels/ckstats:latest" {
		t.Fatalf("ckstats: %q", cp.CkstatsImageTag)
	}
	if cp.DBImageTag != "postgres:16.13-alpine" {
		t.Fatalf("db: %q", cp.DBImageTag)
	}
}

func TestExtractParams_Unknown(t *testing.T) {
	_, err := ExtractParams("unknown", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractParams_MissingTag(t *testing.T) {
	_, err := ExtractParams("bitcoind", "no image here")
	if err == nil {
		t.Fatal("expected error for missing tag")
	}
}

// TestExtractParams_Mempool_OldShape exercises ExtractParams against a verbatim
// v0.3.1-dev.13 mempool compose file (no volumes/healthcheck on backend, no
// healthcheck on frontend, NODE_OPTIONS=1280, mem 1536M). The reconciler reads
// the live compose to extract image tags, then renders the new template — this
// test confirms a user upgrading from dev.13 doesn't get an extract error.
func TestExtractParams_Mempool_OldShape(t *testing.T) {
	dev13Compose := `# Project Truffels — mempool.space (Block Explorer)
# Managed by truffels. Do not edit manually.

services:
  mempool-backend:
    image: mempool/backend:v3.3.1
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
      NODE_OPTIONS: "--max-old-space-size=1280"
      MEMPOOL_BACKEND: "electrum"
    depends_on:
      mempool-db:
        condition: service_healthy
    deploy:
      resources:
        limits:
          memory: 1536M

  mempool-frontend:
    image: mempool/frontend:v3.3.1
    container_name: truffels-mempool-frontend
    restart: unless-stopped
    deploy:
      resources:
        limits:
          memory: 256M

  mempool-db:
    image: mariadb:lts@sha256:78a5047d3ba33975f183f183c2464cc7f1eab13ec8667e57cc9a5821d6da7577
    container_name: truffels-mempool-db

networks:
  bitcoin-backend:
    external: true
`
	p, err := ExtractParams("mempool", dev13Compose)
	if err != nil {
		t.Fatalf("ExtractParams should succeed on dev.13-shape compose: %v", err)
	}
	mp := p.(MempoolParams)
	if mp.BackendImageTag != "mempool/backend:v3.3.1" {
		t.Errorf("backend: %q", mp.BackendImageTag)
	}
	if mp.FrontendImageTag != "mempool/frontend:v3.3.1" {
		t.Errorf("frontend: %q", mp.FrontendImageTag)
	}
	if !contains(mp.DBImageTag, "mariadb:lts") {
		t.Errorf("db: %q", mp.DBImageTag)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestExtractParams_Truffels(t *testing.T) {
	dev14Compose := `services:
  agent:
    image: truffels/agent:v0.3.1-dev.14
    container_name: truffels-agent
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /srv/truffels/compose:/srv/truffels/compose:rw
      - /home/truffel/Project-Truffels:/repo:rw
  api:
    image: truffels/api:v0.3.1-dev.14
    container_name: truffels-api
  web:
    image: truffels/web:v0.3.1-dev.14
    container_name: truffels-web
`
	p, err := ExtractParams("truffels", dev14Compose)
	if err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
	tp := p.(TruffelsParams)
	if tp.AgentTag != "truffels/agent:v0.3.1-dev.14" {
		t.Errorf("agent: %q", tp.AgentTag)
	}
	if tp.APITag != "truffels/api:v0.3.1-dev.14" {
		t.Errorf("api: %q", tp.APITag)
	}
	if tp.WebTag != "truffels/web:v0.3.1-dev.14" {
		t.Errorf("web: %q", tp.WebTag)
	}
	if tp.RepoSrc != "/home/truffel/Project-Truffels" {
		t.Errorf("repo: %q", tp.RepoSrc)
	}
}

func TestExtractParams_Truffels_MissingRepoMount(t *testing.T) {
	compose := `services:
  agent:
    image: truffels/agent:v0.3.1-dev.14
  api:
    image: truffels/api:v0.3.1-dev.14
  web:
    image: truffels/web:v0.3.1-dev.14
`
	_, err := ExtractParams("truffels", compose)
	if err == nil {
		t.Fatal("expected error when /repo:rw mount missing")
	}
}
