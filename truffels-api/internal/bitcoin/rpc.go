package bitcoin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	url        string
	user       string
	pass       string
	httpClient *http.Client
}

func NewClient(host, user, pass string) *Client {
	return &Client{
		url:  "http://" + host,
		user: user,
		pass: pass,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) call(method string, params ...interface{}) (json.RawMessage, error) {
	if params == nil {
		params = []interface{}{}
	}
	body, _ := json.Marshal(rpcRequest{
		JSONRPC: "1.0",
		ID:      "truffels",
		Method:  method,
		Params:  params,
	})

	req, err := http.NewRequest("POST", c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rpc call %s: %w", method, err)
	}
	defer resp.Body.Close()

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("rpc decode %s: %w", method, err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %s: %s", method, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

type BlockchainInfo struct {
	Chain                string  `json:"chain"`
	Blocks               int     `json:"blocks"`
	Headers              int     `json:"headers"`
	BestBlockHash        string  `json:"bestblockhash"`
	Difficulty           float64 `json:"difficulty"`
	VerificationProgress float64 `json:"verificationprogress"`
	SizeOnDisk           int64   `json:"size_on_disk"`
	Pruned               bool    `json:"pruned"`
}

type NetworkInfo struct {
	Version        int    `json:"version"`
	SubVersion     string `json:"subversion"`
	ProtocolVersion int   `json:"protocolversion"`
	Connections    int    `json:"connections"`
	ConnectionsIn  int    `json:"connections_in"`
	ConnectionsOut int    `json:"connections_out"`
}

type MempoolInfo struct {
	Size           int     `json:"size"`
	Bytes          int     `json:"bytes"`
	Usage          int     `json:"usage"`
	TotalFee       float64 `json:"total_fee"`
	MempoolMinFee  float64 `json:"mempoolminfee"`
	MinRelayTxFee  float64 `json:"minrelaytxfee"`
}

func (c *Client) GetBlockchainInfo() (*BlockchainInfo, error) {
	raw, err := c.call("getblockchaininfo")
	if err != nil {
		return nil, err
	}
	var info BlockchainInfo
	return &info, json.Unmarshal(raw, &info)
}

func (c *Client) GetNetworkInfo() (*NetworkInfo, error) {
	raw, err := c.call("getnetworkinfo")
	if err != nil {
		return nil, err
	}
	var info NetworkInfo
	return &info, json.Unmarshal(raw, &info)
}

func (c *Client) GetMempoolInfo() (*MempoolInfo, error) {
	raw, err := c.call("getmempoolinfo")
	if err != nil {
		return nil, err
	}
	var info MempoolInfo
	return &info, json.Unmarshal(raw, &info)
}

type Stats struct {
	Blockchain *BlockchainInfo `json:"blockchain"`
	Network    *NetworkInfo    `json:"network"`
	Mempool    *MempoolInfo    `json:"mempool"`
}

func (c *Client) GetStats() (*Stats, error) {
	bc, err := c.GetBlockchainInfo()
	if err != nil {
		return nil, err
	}
	net, err := c.GetNetworkInfo()
	if err != nil {
		return nil, err
	}
	mp, err := c.GetMempoolInfo()
	if err != nil {
		return nil, err
	}
	return &Stats{
		Blockchain: bc,
		Network:    net,
		Mempool:    mp,
	}, nil
}
