package config

import (
	"fmt"
	"os"
)

type NetworkConfig struct {
	RPCUrl  string
	IsLocal bool
	ChainID int64
}

func GetNetwork() (*NetworkConfig, error) {
	network := os.Getenv("NETWORK")

	switch network {
	case "hardhat":
		return &NetworkConfig{
			RPCUrl:  "ws://127.0.0.1:8545",
			IsLocal: true,
			ChainID: 31337,
		}, nil
	case "sepolia":
		apiKey := os.Getenv("ALCHEMY_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("ALCHEMY_API_KEY is required for sepolia network")
		}
		return &NetworkConfig{
			RPCUrl:  "wss://eth-sepolia.g.alchemy.com/v2/" + apiKey,
			IsLocal: false,
			ChainID: 11155111,
		}, nil
	case "":
		return nil, fmt.Errorf("NETWORK env var is required (set to 'hardhat' or 'sepolia')")
	default:
		return nil, fmt.Errorf("unknown NETWORK: %q (expected 'hardhat' or 'sepolia')", network)
	}
}
