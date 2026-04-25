# EventIndexer

Event indexer for Ethereum smart contracts (stage 2 of Smart-Contracts-Go learning path).

## Setup

```bash
make winner       # generate Go bindings from Solidity
go test ./...     # run unit tests
```

## Demo

Requires a Hardhat node running at `http://127.0.0.1:8545`:

```bash
go run ./cmd/seed/
```

Outputs decoded custom errors instead of bare `execution reverted`.