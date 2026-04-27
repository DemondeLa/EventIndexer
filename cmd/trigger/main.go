package main

import (
	"EventIndexer/abigen/winner"
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
	rpcURL   = "http://127.0.0.1:8545"
	chainID  = 31337
	aliceHex = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	bobHex   = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	carolHex = "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
)

func main() {
	ctx := context.Background()

	// 1. 连接节点
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal(err)
	}

	// 2. 准备TxOpts
	aliceKey, err := crypto.HexToECDSA(strings.TrimPrefix(aliceHex, "0x"))
	if err != nil {
		log.Fatal(err)
	}
	aliceTxOpts, err := bind.NewKeyedTransactorWithChainID(aliceKey, big.NewInt(chainID))
	if err != nil {
		log.Fatal(err)
	}

	bobKey, err := crypto.HexToECDSA(strings.TrimPrefix(bobHex, "0x"))
	if err != nil {
		log.Fatal(err)
	}
	bobTxOpts, err := bind.NewKeyedTransactorWithChainID(bobKey, big.NewInt(chainID))
	if err != nil {
		log.Fatal(err)
	}

	carolKey, err := crypto.HexToECDSA(strings.TrimPrefix(carolHex, "0x"))
	if err != nil {
		log.Fatal(err)
	}
	carolTxOpts, err := bind.NewKeyedTransactorWithChainID(carolKey, big.NewInt(chainID))
	if err != nil {
		log.Fatal(err)
	}

	// 3. 部署合约
	chainNow := time.Now().Unix()
	deadlineSubmit := big.NewInt(chainNow + 3600)
	deadlineVote := big.NewInt(chainNow + 7200)
	addr, deployTx, contract, err := winner.DeployWinnerTakesAll(
		NewTxOpts(*aliceTxOpts, nil),
		client, deadlineSubmit, deadlineVote)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Deployed WinnerTakesAll contract address: %s\n", addr.Hex())
	fmt.Printf("Deployed WinnerTakesAll contract tx: %s\n", deployTx.Hash().Hex())
	_, err = bind.WaitMined(ctx, client, deployTx)
	if err != nil {
		log.Fatal(err)
	}

	// 4. ⬇️ 在这里打断点 ⬇️
	//    在 IDE 里把断点设在下面这一行
	//    手动点击"继续"才会执行 SubmitProject
	fmt.Println("⏸️  按下断点继续，会触发 SubmitProject...")

	// 5. 提交项目 3个用户分别提交一个项目（触发 ProjectSubmitted）
	aliceSubmitTx, err := contract.SubmitProject(
		NewTxOpts(*aliceTxOpts, nil),
		"Alice's Project",
		"https://alice.example",
	)
	if err != nil {
		log.Fatal(err)
	}
	_, err = bind.WaitMined(ctx, client, aliceSubmitTx)
	if err != nil {
		log.Fatal(err)
	}

	bobSubmitTx, err := contract.SubmitProject(
		NewTxOpts(*bobTxOpts, nil),
		"Bob's Project",
		"https://bob.example",
	)
	if err != nil {
		log.Fatal(err)
	}
	_, err = bind.WaitMined(ctx, client, bobSubmitTx)
	if err != nil {
		log.Fatal(err)
	}

	carolSubmitTx, err := contract.SubmitProject(
		NewTxOpts(*carolTxOpts, nil),
		"Carol's Project",
		"https://carol.example",
	)
	if err != nil {
		log.Fatal(err)
	}
	_, err = bind.WaitMined(ctx, client, carolSubmitTx)
	if err != nil {
		log.Fatal(err)
	}

	// 6. 投票阶段时间偏移
	err = client.Client().CallContext(ctx, nil, "evm_increaseTime", 4000)
	if err != nil {
		log.Fatal(err)
	}
	err = client.Client().CallContext(ctx, nil, "evm_mine")
	if err != nil {
		log.Fatal(err)
	}

	// 7. 投票
	aliceVoteTx, err := contract.VoteForProject(
		NewTxOpts(*aliceTxOpts, Eth(2)),
		big.NewInt(1), // 投 Bob 的项目
	)
	if err != nil {
		log.Fatal(err)
	}
	_, err = bind.WaitMined(ctx, client, aliceVoteTx)
	if err != nil {
		log.Fatal(err)
	}

	bobVoteTx, err := contract.VoteForProject(
		NewTxOpts(*bobTxOpts, Eth(3)),
		big.NewInt(0), // 投 Alice 的项目
	)
	if err != nil {
		log.Fatal(err)
	}
	_, err = bind.WaitMined(ctx, client, bobVoteTx)
	if err != nil {
		log.Fatal(err)
	}

	carolVoteTx, err := contract.VoteForProject(
		NewTxOpts(*carolTxOpts, Eth(1)),
		big.NewInt(0), // 投 Alice 的项目
	)
	if err != nil {
		log.Fatal(err)
	}
	_, err = bind.WaitMined(ctx, client, carolVoteTx)
	if err != nil {
		log.Fatal(err)
	}

	// 8. 结算阶段时间偏移
	err = client.Client().CallContext(ctx, nil, "evm_increaseTime", 4000)
	if err != nil {
		log.Fatal(err)
	}
	err = client.Client().CallContext(ctx, nil, "evm_mine")
	if err != nil {
		log.Fatal(err)
	}

	// 9. Carol调用结算函数
	closeTx, err := contract.CloseRound(NewTxOpts(*carolTxOpts, nil))
	if err != nil {
		log.Fatal(err)
	}
	_, err = bind.WaitMined(ctx, client, closeTx)
	if err != nil {
		log.Fatal(err)
	}
}

func Eth(amount int64) *big.Int {
	weiPerEth := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	return new(big.Int).Mul(big.NewInt(amount), weiPerEth)
}

func NewTxOpts(txOpts bind.TransactOpts, value *big.Int) *bind.TransactOpts {
	txOpts.Value = value
	return &txOpts
}
