package main

import (
	"EventIndexer/abigen/winner"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	rpcURL     = "ws://127.0.0.1:8545"
	privateKey = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	chainID    = 31337
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("👋 收到信号: %v", sig)
		cancel()
	}()

	// 1. 获取合约地址
	if len(os.Args) < 2 {
		log.Fatal("用法: go run ./cmd/watch <contract_address>")
	}
	addr := common.HexToAddress(os.Args[1])

	// 2. 连接节点
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// 3. 创建合约实例
	contract, err := winner.NewWinnerTakesAll(addr, client)
	if err != nil {
		log.Fatal(err)
	}

	// 4. 创建channel
	sink := make(chan *winner.WinnerTakesAllProjectSubmitted, 16)

	// 5. 订阅事件
	sub, err := contract.WatchProjectSubmitted(
		&bind.WatchOpts{Context: ctx},
		sink, nil, nil,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer sub.Unsubscribe()

	// 6. 监听事件
	fmt.Println("👀 开始监听...")

	for {
		select {
		case event := <-sink:
			fmt.Println("收到事件: ", event)
		case err := <-sub.Err():
			// TODO: 处理 err == nil 的情况（订阅主动结束）
			log.Printf("订阅出错: %v", err)
			return
		case <-ctx.Done():
			fmt.Println("结束监听")
			return
		}
	}

}
