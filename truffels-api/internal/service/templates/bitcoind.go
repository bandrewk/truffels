package templates

import "truffels-api/internal/model"

var Bitcoind = model.ServiceTemplate{
	ID:             "bitcoind",
	DisplayName:    "Bitcoin Core",
	Description:    "Full Bitcoin node with transaction index, RPC, and ZMQ",
	ComposeDir:     "bitcoin",
	ContainerNames: []string{"truffels-bitcoind"},
	Dependencies:   nil,
	MemoryLimit:    "3500M",
	ConfigPath:     "bitcoin/bitcoin.conf",
	Port:           "8333 (P2P)",
	UpdateSource: &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"btcpayserver/bitcoin"},
	},
}
