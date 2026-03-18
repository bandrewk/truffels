package compose

import (
	"strings"
	"testing"
)

func TestRender_Bitcoin(t *testing.T) {
	got, err := Render("bitcoind", BitcoinParams{ImageTag: "btcpayserver/bitcoin:30.2"})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: btcpayserver/bitcoin:30.2")
	assertContains(t, got, "container_name: truffels-bitcoind")
	assertContains(t, got, "memory: 3500M")
}

func TestRender_Electrs(t *testing.T) {
	got, err := Render("electrs", ElectrsParams{ImageTag: "getumbrel/electrs:v0.11.0"})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: getumbrel/electrs:v0.11.0")
	assertContains(t, got, "container_name: truffels-electrs")
	assertContains(t, got, "memory: 2048M")
}

func TestRender_Ckpool(t *testing.T) {
	got, err := Render("ckpool", CkpoolParams{ImageTag: "truffels/ckpool:v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: truffels/ckpool:v1.0.0")
	assertContains(t, got, "memory: 1024M")
	assertContains(t, got, "container_name: truffels-ckpool")
}

func TestRender_Mempool(t *testing.T) {
	got, err := Render("mempool", MempoolParams{
		BackendImageTag:  "mempool/backend:v3.2.1",
		FrontendImageTag: "mempool/frontend:v3.2.1",
		DBImageTag:       "mariadb:lts@sha256:abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: mempool/backend:v3.2.1")
	assertContains(t, got, "image: mempool/frontend:v3.2.1")
	assertContains(t, got, "image: mariadb:lts@sha256:abc123")
	assertContains(t, got, "container_name: truffels-mempool-backend")
}

func TestRender_Ckstats(t *testing.T) {
	got, err := Render("ckstats", CkstatsParams{
		CkstatsImageTag: "truffels/ckstats:latest",
		DBImageTag:       "postgres:16.13-alpine",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: truffels/ckstats:latest")
	assertContains(t, got, "image: postgres:16.13-alpine")
	assertContains(t, got, "container_name: truffels-ckstats-db")
}

func TestRender_Proxy(t *testing.T) {
	got, err := Render("proxy", ProxyParams{ImageTag: "caddy:2.11.2-alpine"})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: caddy:2.11.2-alpine")
	assertContains(t, got, "container_name: truffels-proxy")
	assertContains(t, got, "memory: 128M")
}

func TestRender_UnknownService(t *testing.T) {
	_, err := Render("unknown", nil)
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q, got:\n%s", substr, s)
	}
}
