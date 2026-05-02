package main

import (
	"EventIndexer/abigen/winner"
	"EventIndexer/internal/db"
	"EventIndexer/internal/indexer"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

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

	// init DB
	cfg := db.DefaultConfig()
	database, err := db.Connect(cfg)
	if err != nil {
		log.Fatalf("connect to the postgres failed: %v", err)
	}
	defer database.Close()

	repo := db.NewRepo(database)

	// 1. 获取合约地址
	if len(os.Args) < 2 {
		log.Fatal("用法: go run ./cmd/watch <contract_address>")
	}
	addr := common.HexToAddress(os.Args[1])

	// 2. 连接节点
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatalf("connect to the Ethereum client failed: %v", err)
	}
	defer client.Close()

	// 3. 创建合约实例
	contract, err := winner.NewWinnerTakesAll(addr, client)
	if err != nil {
		log.Fatalf("get contract instance failed: %v", err)
	}

	/*
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
			case err, ok := <-sub.Err():
				// chan close时返回零值，用ok显式区分chan内容为nil还是close
				// 理论上 channel 还没 close 时也可以传一个 nil 进去
				// ok 直接问的是channel本身的状态，而err == nil问的是传来的值
				if !ok {
					// channel被close了 —— 主动退订（比如 sub.Unsubscribe() 被调用）
					log.Println("订阅 channel 已关闭，主动退订")
					return
				}
				if err != nil {
					// 真正的错误（连接断了、节点出问题等）
					log.Printf("订阅出错: %v", err)
					return
				}
			case <-ctx.Done():
				fmt.Println("结束监听")
				return
			}
		}

	*/

	idx, err := indexer.NewIndexer(contract)
	if err != nil {
		log.Fatalf("init indexer failed: %v", err)
	}

	// 读 DB：上次同步到哪一块
	lastSynced, err := repo.GetLastSyncedBlock(ctx)
	if err != nil {
		log.Fatalf("get last synced block failed: %v", err)
	}

	// 读链：当前最新块
	// 这里只需要块号，用 BlockNumber 比 HeaderByNumber 流量更省（只返回数字而不是整个区块头）
	currentBlock, err := client.BlockNumber(ctx)
	if err != nil {
		log.Fatalf("get current block failed: %v", err)
	}

	// 定义 callback（Sync 和 Run 共用）
	onEvent := func(e indexer.Event) error {
		// onEvent 回调："事件来了打印一下"
		fmt.Printf("🔔 [块 %d] projectId=%d name=%q submitter=%s tx=%s\n",
			e.BlockNumber, e.ProjectID, e.Name, e.Submitter, e.TxHash[:10]+"...")
		//time.Sleep(3 * time.Second) // ← 故意慢 3 秒，做反压实验(将2个chan buffer改为1)
		err := repo.InsertEvent(ctx, e)
		if err != nil {
			log.Printf("insert failed: tx=%s block=%d err=%v", e.TxHash, e.BlockNumber, err)
			return err
		}
		return repo.UpdateLastSyncedBlock(ctx, e.BlockNumber)
	}

	// 决策：要不要 Sync？
	if lastSynced <= currentBlock {
		fmt.Printf("⛳ 上次同步到块 %d，现在最新块是 %d\n", lastSynced, currentBlock)

		err = idx.Sync(ctx, lastSynced, currentBlock, onEvent)
		if err != nil {
			log.Fatalf("initial sync failed: %v", err)
		}
		/* Sync 完后显式 UpdateLastSyncedBlock(ctx, currentBlock)
		   当前依赖 onEvent 推进 sync_state，导致"扫描了空块但 sync_state 不更新"
		   本地 Hardhat 块少没影响，Sepolia/主网回填时必须改
		*/
		err = repo.UpdateLastSyncedBlock(ctx, currentBlock)
		if err != nil {
			log.Fatalf("sync last block failed: %v", err)
		}
	} else {
		log.Println("✅ 历史数据已是最新，跳过同步")
	}

	err = idx.Run(ctx, onEvent)
	if err != nil {
		log.Fatalf("start indexer failed: %v", err)
	}
}
