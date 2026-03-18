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
