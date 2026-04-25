package main

import (
	"EventIndexer/abigen/winner"
	"EventIndexer/internal/abiutil"
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	rpcURL     = "http://127.0.0.1:8545"
	privateKey = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	chainID    = 31337
)

func main() {
	ctx := context.Background()

	// 1. 连接节点
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal(err)
	}

	// 2. 准备 TxOpts
	key, err := crypto.HexToECDSA(strings.TrimPrefix(privateKey, "0x"))
	if err != nil {
		log.Fatal(err)
	}
	txOpts, err := bind.NewKeyedTransactorWithChainID(key, big.NewInt(chainID))
	if err != nil {
		log.Fatal(err)
	}

	// 3. 部署合约
	//    deadlineSubmit 给个未来时间（比如 now + 3600）
	now := time.Now().Unix()
	deadlineSubmit := big.NewInt(now + 3600) // 1 小时后
	//    deadlineVote 给比 submit 更晚的时间
	deadlineVote := big.NewInt(now + 7200) // 2 小时后
	addr, deployTx, contract, err := winner.DeployWinnerTakesAll(txOpts, client, deadlineSubmit, deadlineVote)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Deployed contract address: %s\n", addr.Hex())
	fmt.Printf("Deployed contract tx: %s\n", deployTx.Hash().Hex())
	//    等部署上链
	_, err = bind.WaitDeployed(ctx, client, deployTx)
	if err != nil {
		log.Fatal(err)
	}

	// 4. 创建 Decoder
	decoder, err := abiutil.NewDecoder(winner.WinnerTakesAllMetaData.ABI)
	if err != nil {
		log.Fatal(err)
	}

	// 5. Demo 1: InvalidProjectId
	fmt.Println("========== Demo 1: VotePhaseNotActive (modifier 先 revert) ==========")
	_, err = contract.VoteForProject(txOpts, big.NewInt(999))

	fmt.Println("原始 err:   ", err)

	// ↓↓↓ 临时诊断代码 ↓↓↓
	fmt.Printf("err 实际类型: %T\n", err)
	if de, ok := err.(interface{ ErrorData() interface{} }); ok {
		fmt.Printf("实现了 ErrorData() 接口\n")
		data := de.ErrorData()
		fmt.Printf("  ErrorData() 返回类型: %T\n", data)
		fmt.Printf("  ErrorData() 返回值: %v\n", data)
	} else {
		fmt.Printf("没有实现 ErrorData() 接口\n")
	}
	// ↑↑↑ 临时诊断代码 ↑↑↑

	fmt.Println("解码后 err: ", decoder.Decode(err))

	// 6. Demo 2: InvalidProjectId
	fmt.Println("========== Demo 2: InvalidProjectId ==========")
	// 先推进区块时间
	err = client.Client().CallContext(ctx, nil, "evm_increaseTime", 4000)
	if err != nil {
		log.Fatal(err)
	}
	err = client.Client().CallContext(ctx, nil, "evm_mine")
	if err != nil {
		log.Fatal(err)
	}
	_, err = contract.VoteForProject(txOpts, big.NewInt(999))

	fmt.Println("原始 err:   ", err)

	fmt.Println("解码后 err: ", decoder.Decode(err))
}
