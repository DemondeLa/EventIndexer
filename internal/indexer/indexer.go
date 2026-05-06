package indexer

import (
	"EventIndexer/abigen/winner"
	"EventIndexer/internal/db"
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
)

type Event struct {
	ProjectID uint64
	Name      string
	URL       string
	Submitter string
	TxHash    string
	LogIndex  uint64 // 转换时记得 uint64(raw.Raw.Index)
	// LogIndex abigen是uint, 这里固定uint64
	BlockNumber uint64
}

type Indexer struct {
	contract *winner.WinnerTakesAll // ← abigen 生成的合约 binding
	repo     *db.Repo
	client   *ethclient.Client
}

func NewIndexer(contract *winner.WinnerTakesAll, r *db.Repo, c *ethclient.Client) (*Indexer, error) {
	if contract == nil {
		return nil, errors.New("contract is nil") // 防御
	}

	return &Indexer{
		contract: contract,
		repo:     r,
		client:   c,
	}, nil
}

func convertToEvent(raw *winner.WinnerTakesAllProjectSubmitted) Event {
	return Event{
		ProjectID:   raw.ProjectId.Uint64(), // abigen 生成的 *big.Int 转 uint64
		Name:        raw.Name,
		URL:         raw.Url,
		Submitter:   raw.Submitter.Hex(), // common.Address 转 string
		TxHash:      raw.Raw.TxHash.Hex(),
		LogIndex:    uint64(raw.Raw.Index), // abigen 生成的 uint 转 uint64
		BlockNumber: raw.Raw.BlockNumber,
	}
}

func (idx *Indexer) Sync(ctx context.Context, fromBlock, toBlock uint64, onEvent func(Event) error) error {
	opts := &bind.FilterOpts{
		Start:   fromBlock,
		End:     &toBlock,
		Context: ctx,
	}

	iter, err := idx.contract.FilterProjectSubmitted(opts, nil, nil)
	if err != nil {
		return fmt.Errorf("filter events: %w", err)
	}
	defer iter.Close() // 必须释放

	count := 0
	for iter.Next() { // 推进游标
		count++
		ev := convertToEvent(iter.Event)
		if err := onEvent(ev); err != nil {
			log.Printf("处理失败 (tx=%s block=%d projectId=%d): %v",
				ev.TxHash, ev.BlockNumber, ev.ProjectID, err)
			return err
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("iterate events: %w", iter.Error())
	}

	log.Printf("✅ 历史同步完成，共 %d 条", count)
	return nil
}

func (idx *Indexer) syncBeforeWatch(ctx context.Context, onEvent func(Event) error) error {
	// 1. 读 sync_state 上次同步到哪一块
	lastSynced, err := idx.repo.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("get last synced block: %w", err)
	}

	// 2. 查最新块（用 idx.client.BlockNumber）
	currentBlock, err := idx.client.BlockNumber(ctx)
	// 这里只需要块号，用 BlockNumber 比 HeaderByNumber 流量更省（只返回数字而不是整个区块头）
	if err != nil {
		return fmt.Errorf("get current block: %w", err)
	}

	// 3. 决定要不要 sync（Day 5 的逻辑：if lastSynced <= currentBlock）
	if lastSynced > currentBlock {
		log.Println("✅ 历史数据已是最新，跳过同步")
		return nil
	}

	fmt.Printf("⛳ 上次同步到块 %d，现在最新块是 %d\n", lastSynced, currentBlock)

	// 4. 调 idx.Sync(ctx, lastSynced+1, currentBlock, onEvent)
	err = idx.Sync(ctx, lastSynced, currentBlock, onEvent)
	if err != nil {
		return fmt.Errorf("initial sync: %w", err)
	}
	// 5. 调 db.UpdateLastSyncedBlock 更新进度
	err = idx.repo.UpdateLastSyncedBlock(ctx, currentBlock)
	if err != nil {
		return fmt.Errorf("update last block: %w", err)
	}

	return nil
}

func (idx *Indexer) runSession(ctx context.Context, onEvent func(Event) error) error {
	var wg sync.WaitGroup
	wg.Add(2)

	// 1. 创建 sink，订阅链上事件
	sink := make(chan *winner.WinnerTakesAllProjectSubmitted, 32) // 32 是缓冲区大小，按需调整

	sub, err := idx.contract.WatchProjectSubmitted(
		&bind.WatchOpts{Context: ctx},
		sink, nil, nil,
	)
	if err != nil {
		return fmt.Errorf("订阅失败: %w", err)
	}
	fmt.Println("👀 开始监听...")

	// 2. 创建业务 channel
	eventCh := make(chan Event, 32)
	var runErr error

	// 3. 生产者 goroutine
	go func() {
		defer wg.Done()
		defer close(eventCh)
		defer sub.Unsubscribe()

		for {
			select {
			case raw := <-sink:
				e := convertToEvent(raw)
				select {
				case eventCh <- e:
					//fmt.Println("收到事件: ", e)
				case <-ctx.Done():
					fmt.Println("结束监听")
					return
				}
			case err, ok := <-sub.Err():
				// chan close时返回零值，用ok显式区分chan内容为nil还是close：
				// 理论上channel还没close时也可以传一个nil进去
				// ok 直接问的是channel本身的状态，而err == nil问的是传来的值
				if !ok {
					log.Println("订阅 channel 已关闭，主动退订")
					return
				}
				if err != nil {
					// 真正的错误（连接断了、节点出问题等）
					log.Printf("订阅出错: %v", err)
					runErr = err
					return
				}
			case <-ctx.Done():
				fmt.Println("结束监听")
				return
			}
		}
	}()

	// 4. 消费者 goroutine
	go func() {
		defer wg.Done()
		for event := range eventCh {
			if err := onEvent(event); err != nil {
				log.Printf("处理失败 (projectId=%d): %v",
					event.ProjectID, err)
				//log.Printf("处理失败 (tx=%s block=%d projectId=%d): %v",
				//	event.TxHash, event.BlockNumber, event.ProjectID, err)
			}
		}
	}()

	// 5. 等待上下文结束，优雅退出
	wg.Wait()
	return runErr
}

func (idx *Indexer) Run(ctx context.Context, onEvent func(Event) error) error {
	const maxRetries = 10
	const retryWait = 5 * time.Second
	retries := 0

	for {
		// 先 sync 历史
		if err := idx.syncBeforeWatch(ctx, onEvent); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if retries >= maxRetries {
				return fmt.Errorf("retry budget exhausted (sync): %w", err)
			}
			retries++
			log.Printf("[WARN] sync 失败: %v，%s 后重试（%d/%d）", err, retryWait, retries, maxRetries)
			select {
			case <-time.After(retryWait):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		err := idx.runSession(ctx, onEvent)

		// 先 check ctx（可能是用户取消了），再 check 错误（可能是网络问题等）
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// runSession 正常返回 → 退出
		if err == nil {
			return nil
		}
		// err != nil 且 ctx 没取消 → 是 transient 错误
		if retries >= maxRetries {
			log.Printf("[ERROR] 重试预算耗尽（%d 次），放弃", maxRetries)
			return fmt.Errorf("retry budget exhausted: %w", err)
		}
		retries++
		log.Printf("[WARN] runSession 失败: %v，%s 后重试（%d/%d）",
			err, retryWait, retries, maxRetries)

		// sleep（ctx-aware）
		select {
		case <-time.After(retryWait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
