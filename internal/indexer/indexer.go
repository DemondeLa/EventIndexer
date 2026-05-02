package indexer

import (
	"EventIndexer/abigen/winner"
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
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
}

func NewIndexer(contract *winner.WinnerTakesAll) (*Indexer, error) {
	if contract == nil {
		return nil, errors.New("contract is nil") // 防御
	}
	// 未来可能：验证合约 ABI、ping 一下节点、加载配置...
	return &Indexer{contract: contract}, nil
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

func (idx *Indexer) Run(ctx context.Context, onEvent func(Event) error) error {
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
	return nil
}
