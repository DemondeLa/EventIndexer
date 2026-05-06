package main

import (
	"EventIndexer/abigen/winner"
	"EventIndexer/internal/account"
	"EventIndexer/internal/config"
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load() // 在程序启动时把.env注入os环境变量
	fmt.Println("NETWORK =", os.Getenv("NETWORK"))
	ctx := context.Background()

	// 1. 连接节点
	cfg, err := config.GetNetwork()
	if err != nil {
		log.Fatalf("get network failed: %v", err)
	}
	rpcURL := cfg.RPCUrl
	chainID := big.NewInt(cfg.ChainID)
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatalf("connect to the Ethereum client failed: %v", err)
	}

	// 2. 准备TxOpts
	keyHex := os.Getenv("PRIVATE_KEY")
	if keyHex == "" {
		log.Fatal("PRIVATE_KEY env var is required")
	}
	hexKey := strings.TrimPrefix(keyHex, "0x")
	myKey, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		log.Fatalf("get private key failed: %v", err)
	}
	myTxOpts, err := bind.NewKeyedTransactorWithChainID(myKey, chainID)
	if err != nil {
		log.Fatalf("new keyed transactor failed: %v", err)
	}

	// 3. 部署合约
	chainNow := time.Now()
	deadlineSubmit := big.NewInt(chainNow.Add(24 * time.Hour).Unix())
	deadlineVote := big.NewInt(chainNow.Add(48 * time.Hour).Unix())

	myDeployTxOpts := NewTxOpts(*myTxOpts, nil)
	if err := account.SetEIP1559Gas(ctx, client, myDeployTxOpts); err != nil {
		log.Fatalf("set gas failed: %v", err)
	}

	addr, deployTx, contract, err := winner.DeployWinnerTakesAll(
		myDeployTxOpts, client, deadlineSubmit, deadlineVote)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Deployed WinnerTakesAll contract address: %s\n", addr.Hex())
	fmt.Printf("Deployed WinnerTakesAll contract tx: %s\n", deployTx.Hash().Hex())
	_, err = bind.WaitMined(ctx, client, deployTx)
	if err != nil {
		log.Fatalf("wait for deploy tx mined failed: %v", err)
	}
	//addr := common.HexToAddress("0x4Db599F92d16d1a304D67045664A1C27caC492f5")
	//contract, err := winner.NewWinnerTakesAll(addr, client)
	//if err != nil {
	//	log.Fatalf("get contract instance failed: %v", err)
	//}

	// 打印Sepolia当前区块号
	blockNumber, err := client.BlockNumber(ctx)
	if err != nil {
		log.Fatalf("get block number failed: %v", err)
	}
	fmt.Printf("current block number is %d\n", blockNumber)

	// 5. 提交项目 (Day6:做最小验证，只提交一个项目)
	mySubmitTxOpts := NewTxOpts(*myTxOpts, nil)
	if err := account.SetEIP1559Gas(ctx, client, mySubmitTxOpts); err != nil {
		log.Fatalf("set gas failed: %v", err)
	}
	mySubmitTx, err := contract.SubmitProject(
		mySubmitTxOpts,
		"My Project",
		"https://my.example",
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Submit tx: %s\n", mySubmitTx.Hash().Hex())
	_, err = bind.WaitMined(ctx, client, mySubmitTx)
	if err != nil {
		log.Fatal(err)
	}

	// 打印Sepolia当前区块号
	blockNumber, err = client.BlockNumber(ctx)
	if err != nil {
		log.Fatalf("get block number failed: %v", err)
	}
	fmt.Printf("after submit1, current block number is %d\n", blockNumber)

	//    在 IDE 里把断点设在下面这一行
	//    手动点击"继续"才会执行 SubmitProject
	fmt.Println("⏸️  按下断点继续，会触发 SubmitProject...")

	mySubmitTxOpts2 := NewTxOpts(*myTxOpts, nil)
	if err := account.SetEIP1559Gas(ctx, client, mySubmitTxOpts2); err != nil {
		log.Fatalf("set gas failed: %v", err)
	}
	mySubmitTx2, err := contract.SubmitProject(
		mySubmitTxOpts2,
		"My Project2",
		"https://my2.example",
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Submit tx: %s\n", mySubmitTx2.Hash().Hex())
	_, err = bind.WaitMined(ctx, client, mySubmitTx2)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("[INFO] Sepolia 模式：仅跑部署 + 2 笔 submit，跳过投票和结算")
	log.Println("[TODO] Day 7 用 --minimal flag 重构")
}

func NewTxOpts(txOpts bind.TransactOpts, value *big.Int) *bind.TransactOpts {
	txOpts.Value = value
	return &txOpts
}
