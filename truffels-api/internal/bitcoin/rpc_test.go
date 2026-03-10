package bitcoin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mockRPCServer(t *testing.T, handler func(method string) (interface{}, *rpcError)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check basic auth
		user, pass, ok := r.BasicAuth()
		if !ok || user != "testuser" || pass != "testpass" {
			w.WriteHeader(401)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var req rpcRequest
		json.Unmarshal(body, &req)

		result, rpcErr := handler(req.Method)
		resp := rpcResponse{}
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			resp.Result, _ = json.Marshal(result)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestGetBlockchainInfo(t *testing.T) {
	srv := mockRPCServer(t, func(method string) (interface{}, *rpcError) {
		if method != "getblockchaininfo" {
			return nil, &rpcError{Code: -32601, Message: "Method not found"}
		}
		return BlockchainInfo{
			Chain:                "main",
			Blocks:               890123,
			Headers:              890123,
			BestBlockHash:        "0000000000000000000abc",
			Difficulty:           92049594548004.64,
			VerificationProgress: 0.9999,
			SizeOnDisk:           650000000000,
			Pruned:               false,
		}, nil
	})
	defer srv.Close()

	client := NewClient(srv.Listener.Addr().String(), "testuser", "testpass")
	info, err := client.GetBlockchainInfo()
	if err != nil {
		t.Fatalf("get blockchain info: %v", err)
	}
	if info.Chain != "main" {
		t.Fatalf("expected main, got %q", info.Chain)
	}
	if info.Blocks != 890123 {
		t.Fatalf("expected 890123, got %d", info.Blocks)
	}
	if info.Pruned {
		t.Fatal("expected not pruned")
	}
}

func TestGetNetworkInfo(t *testing.T) {
	srv := mockRPCServer(t, func(method string) (interface{}, *rpcError) {
		return NetworkInfo{
			Version:        290000,
			SubVersion:     "/Satoshi:29.0.0/",
			Connections:    15,
			ConnectionsIn:  5,
			ConnectionsOut: 10,
		}, nil
	})
	defer srv.Close()

	client := NewClient(srv.Listener.Addr().String(), "testuser", "testpass")
	info, err := client.GetNetworkInfo()
	if err != nil {
		t.Fatalf("get network info: %v", err)
	}
	if info.Connections != 15 {
		t.Fatalf("expected 15 connections, got %d", info.Connections)
	}
}

func TestGetMempoolInfo(t *testing.T) {
	srv := mockRPCServer(t, func(method string) (interface{}, *rpcError) {
		return MempoolInfo{
			Size:  5000,
			Bytes: 2500000,
			Usage: 10000000,
		}, nil
	})
	defer srv.Close()

	client := NewClient(srv.Listener.Addr().String(), "testuser", "testpass")
	info, err := client.GetMempoolInfo()
	if err != nil {
		t.Fatalf("get mempool info: %v", err)
	}
	if info.Size != 5000 {
		t.Fatalf("expected 5000 txs, got %d", info.Size)
	}
}

func TestGetStats_Aggregation(t *testing.T) {
	srv := mockRPCServer(t, func(method string) (interface{}, *rpcError) {
		switch method {
		case "getblockchaininfo":
			return BlockchainInfo{Chain: "main", Blocks: 890123}, nil
		case "getnetworkinfo":
			return NetworkInfo{Connections: 15}, nil
		case "getmempoolinfo":
			return MempoolInfo{Size: 5000}, nil
		default:
			return nil, &rpcError{Code: -32601, Message: "unknown"}
		}
	})
	defer srv.Close()

	client := NewClient(srv.Listener.Addr().String(), "testuser", "testpass")
	stats, err := client.GetStats()
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.Blockchain == nil || stats.Network == nil || stats.Mempool == nil {
		t.Fatal("expected all three fields populated")
	}
	if stats.Blockchain.Blocks != 890123 {
		t.Fatalf("expected 890123, got %d", stats.Blockchain.Blocks)
	}
}

func TestRPCError(t *testing.T) {
	srv := mockRPCServer(t, func(method string) (interface{}, *rpcError) {
		return nil, &rpcError{Code: -28, Message: "Loading block index..."}
	})
	defer srv.Close()

	client := NewClient(srv.Listener.Addr().String(), "testuser", "testpass")
	_, err := client.GetBlockchainInfo()
	if err == nil {
		t.Fatal("expected error for RPC error")
	}
	if !contains(err.Error(), "Loading block index") {
		t.Fatalf("expected error message about loading, got %q", err.Error())
	}
}

func TestRPCAuthFailure(t *testing.T) {
	srv := mockRPCServer(t, func(method string) (interface{}, *rpcError) {
		return nil, nil
	})
	defer srv.Close()

	client := NewClient(srv.Listener.Addr().String(), "wrong", "creds")
	_, err := client.GetBlockchainInfo()
	if err == nil {
		t.Fatal("expected error for auth failure")
	}
}

func TestRPCConnectionRefused(t *testing.T) {
	client := NewClient("127.0.0.1:1", "user", "pass")
	_, err := client.GetBlockchainInfo()
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
