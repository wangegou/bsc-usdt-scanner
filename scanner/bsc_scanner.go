package scanner

import (
	"context"
	"fmt"
	"math/big"
	"sort" // 引入排序包
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	// TRANSFER_EVENT_SIG 定义 Transfer 事件的哈希签名
	TRANSFER_EVENT_SIG = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	// TOKEN_DECIMALS 定义代币精度 (USDT/USDC 均为 18位)
	TOKEN_DECIMALS = 18

	// WORKER_COUNT 并发工兵数量 (5个刚好，太多会被封IP，太少太慢)
	WORKER_COUNT = 5
)

// SupportedTokens 支持的代币列表
var SupportedTokens = map[string]string{
	"USDT": "0x55d398326f99059fF775485246999027B3197955",
	"USDC": "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d", // Binance-Peg USDC
}

// DefaultRPCs 默认的 RPC 节点列表
var DefaultRPCs = []string{
	"https://binance.llamarpc.com",   // 优先尝试 Llama
	"https://bsc-rpc.publicnode.com", // 备选 PublicNode
	"https://1rpc.io/bnb",            // 备选 1RPC
	"https://bsc.meowrpc.com",        // 备选 Meow
}

// StartScan 扫描入口封装，自动寻找可用节点并返回结果
// walletAddr: 钱包地址
// symbol: 代币符号 (如 "USDT", "USDC")
func StartScan(walletAddr string, symbol string) ([]DepositRecord, error) {
	// 获取代币合约地址
	contractAddr, ok := SupportedTokens[strings.ToUpper(symbol)]
	if !ok {
		return nil, fmt.Errorf("不支持的代币符号: %s", symbol)
	}

	fmt.Printf("🚀 正在寻找最佳 RPC 节点以扫描 %s (%s)...\n", symbol, contractAddr)

	var bsc *TokenScanner
	var currentBlock uint64
	var activeRPC string

	// 遍历 RPC 列表，寻找可用的节点
	for _, rpcUrl := range DefaultRPCs {
		fmt.Printf("   正在测试: %-35s ... ", rpcUrl)

		// 1. 尝试建立连接
		tempScanner, err := NewTokenScanner(rpcUrl, DefaultRPCs, contractAddr)
		if err != nil {
			fmt.Printf("❌ 连接失败 (%v)\n", err)
			continue
		}

		// 2. 尝试实际请求 (3秒超时测速)
		ctxTest, cancelTest := context.WithTimeout(context.Background(), 3*time.Second)
		block, err := tempScanner.GetCurrentBlock(ctxTest)
		cancelTest()

		if err == nil {
			fmt.Println("✅ 响应正常！")
			bsc = tempScanner
			currentBlock = block
			activeRPC = rpcUrl
			break
		} else {
			fmt.Println("❌ 不可用")
			tempScanner.Close()
		}
	}

	if bsc == nil {
		return nil, fmt.Errorf("所有备选节点都无法连接，请检查网络")
	}
	defer bsc.Close()

	fmt.Printf("\n🌟 最终选用节点: %s\n", activeRPC)
	fmt.Printf("📦 当前最新高度: %d\n", currentBlock)

	// 配置扫描任务
	// 设置总任务的超时时间 (1分钟)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	// 设定扫描范围 (30个区块)
	scanRange := uint64(30)
	startBlock := currentBlock - scanRange

	// 执行扫描
	return bsc.ScanDeposits(ctx, walletAddr, startBlock, currentBlock)
}

// DepositRecord 定义入账记录结构体
type DepositRecord struct {
	TxHash      string     // 交易哈希
	BlockNumber uint64     // 区块高度
	From        string     // 发送方地址
	To          string     // 接收方地址
	Amount      *big.Float // 金额
	LogIndex    uint       // 日志索引，用于同区块排序
	Time        time.Time  // 交易时间
}

// TokenScanner 定义扫描器结构体
type TokenScanner struct {
	Client          *ethclient.Client // 以太坊客户端
	ContractAddress common.Address    // 合约地址对象
	TransferTopic   common.Hash       // 事件主题哈希

	// 自动切换节点相关字段
	rpcList    []string     // 所有可用 RPC 列表
	currentRPC string       // 当前正在使用的 RPC
	mu         sync.RWMutex // 保护 Client 和 currentRPC 的读写
}

// NewTokenScanner 创建一个新的扫描器实例
func NewTokenScanner(initialRPC string, allRPCs []string, contractAddr string) (*TokenScanner, error) {
	// 连接到指定的 RPC 节点
	client, err := ethclient.Dial(initialRPC)
	if err != nil {
		// 如果连接失败，返回错误
		return nil, fmt.Errorf("连接 RPC 失败: %w", err)
	}
	// 返回初始化的扫描器对象
	return &TokenScanner{
		Client:          client,
		ContractAddress: common.HexToAddress(contractAddr),
		TransferTopic:   common.HexToHash(TRANSFER_EVENT_SIG),
		rpcList:         allRPCs,
		currentRPC:      initialRPC,
	}, nil
}

// Close 关闭扫描器连接
func (s *TokenScanner) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Client != nil {
		s.Client.Close()
	}
}

// GetCurrentBlock 获取当前最新区块高度
func (s *TokenScanner) GetCurrentBlock(ctx context.Context) (uint64, error) {
	s.mu.RLock()
	client := s.Client
	s.mu.RUnlock()
	return client.BlockNumber(ctx)
}

// switchNode 切换到下一个可用节点
func (s *TokenScanner) switchNode(failedRPC string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double Check: 如果当前节点已经不是失败的那个节点（说明被其他协程切过了），直接返回
	if s.currentRPC != failedRPC {
		return
	}

	fmt.Printf("\n⚠️  节点 %s 遇到限流/故障，正在尝试切换...\n", s.currentRPC)

	// 查找当前节点在列表中的位置
	currentIndex := -1
	for i, rpc := range s.rpcList {
		if rpc == s.currentRPC {
			currentIndex = i
			break
		}
	}

	// 尝试寻找下一个可用节点
	for i := 1; i <= len(s.rpcList); i++ {
		nextIndex := (currentIndex + i) % len(s.rpcList)
		nextRPC := s.rpcList[nextIndex]

		// 简单的去重（虽然逻辑上不会选到自己，除非只有一个）
		if nextRPC == s.currentRPC {
			continue
		}

		fmt.Printf("   >> 尝试连接备选节点: %s ... \n", nextRPC)
		newClient, err := ethclient.Dial(nextRPC)
		if err != nil {
			fmt.Printf("失败 (%v)\n", err)
			continue
		}

		// 测试一下是否真的可用
		ctxTest, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err = newClient.BlockNumber(ctxTest)
		cancel()

		if err != nil {
			newClient.Close()
			fmt.Printf("不可用 (%v)\n", err)
			continue
		}

		fmt.Println("✅ 切换成功！")

		// 关闭旧连接
		if s.Client != nil {
			s.Client.Close()
		}

		// 更新状态
		s.Client = newClient
		s.currentRPC = nextRPC
		return
	}

	fmt.Println("❌ 警告：所有备选节点都尝试失败，继续使用当前节点重试。")
}

// ScanDeposits 并发扫描入口函数
// 参数: 上下文, 钱包地址, 开始区块, 结束区块
// 返回: 入账记录列表, 错误信息
func (s *TokenScanner) ScanDeposits(ctx context.Context, walletAddr string, startBlock, endBlock uint64) ([]DepositRecord, error) {
	// 将钱包地址转换为哈希格式，用于过滤日志
	targetAddressHash := common.HexToHash(walletAddr)

	// 创建任务通道 (存放待扫描的区块号)
	jobs := make(chan uint64, 100)
	// 创建结果通道 (存放扫描到的记录)
	results := make(chan []DepositRecord, 100)

	// 创建 WaitGroup 用于等待所有工兵完成
	var wg sync.WaitGroup

	// 打印启动信息
	fmt.Printf("启动并发引擎: %d 个工兵 | 扫描范围: %d -> %d (共 %d 块)\n",
		WORKER_COUNT, startBlock, endBlock, endBlock-startBlock)

	// 1. 启动工兵 (并发消费者)
	for w := 0; w < WORKER_COUNT; w++ {
		wg.Add(1)
		// 启动每个工兵协程
		go s.worker(ctx, w, jobs, results, &wg, targetAddressHash)
	}

	// 2. 发送任务 (生产者)
	go func() {
		// 遍历区块范围，生成任务
		for i := startBlock; i <= endBlock; i++ {
			select {
			case jobs <- i: // 将区块号发送到任务通道
			case <-ctx.Done(): // 如果上下文取消，停止发送
				return // 使用 return 退出整个协程，break 只能跳出 select
			}
		}
		// 任务发送完毕，关闭任务通道
		close(jobs)
	}()

	// 3. 等待所有工兵完成并关闭结果通道 (清理者)
	go func() {
		wg.Wait()      // 等待所有工兵完成
		close(results) // 关闭结果通道
	}()

	// 4. 收集结果
	var allRecords []DepositRecord
	processedCount := 0
	totalBlocks := endBlock - startBlock + 1

	// 从结果通道不断读取数据
	for res := range results {
		if len(res) > 0 {
			// 将当前块的结果追加到总记录中
			allRecords = append(allRecords, res...)
		}
		processedCount++
		// 每完成 100 个区块打印一次进度
		if processedCount%100 == 0 {
			fmt.Printf("\r>> 进度: %.1f%% (%d/%d)", float64(processedCount)/float64(totalBlocks)*100, processedCount, totalBlocks)
		}
	}
	fmt.Println("\n扫描结束。")

	// -----------------------------------------------------
	// 新增逻辑：对结果进行排序，确保按时间倒序输出 (最新的在前)
	// -----------------------------------------------------
	sort.Slice(allRecords, func(i, j int) bool {
		// 首先按区块高度排序 (从大到小)
		if allRecords[i].BlockNumber != allRecords[j].BlockNumber {
			return allRecords[i].BlockNumber > allRecords[j].BlockNumber
		}
		// 如果区块高度相同，按日志索引排序 (从大到小)
		return allRecords[i].LogIndex > allRecords[j].LogIndex
	})

	// 返回收集并排序后的所有记录
	return allRecords, nil
}

// worker 工兵函数：负责具体的区块扫描逻辑，一次只处理一个区块
func (s *TokenScanner) worker(ctx context.Context, id int, jobs <-chan uint64, results chan<- []DepositRecord, wg *sync.WaitGroup, targetHash common.Hash) {
	// 函数退出时通知 WaitGroup
	defer wg.Done()

	// 循环从 jobs 通道领取任务
	for blockNum := range jobs {
		// 检查上下文是否已取消 (超时或手动停止)
		if ctx.Err() != nil {
			return
		}

		// 1. 构造查询条件
		query := ethereum.FilterQuery{
			FromBlock: big.NewInt(int64(blockNum)), // 当前区块
			ToBlock:   big.NewInt(int64(blockNum)), // 当前区块
			Addresses: nil,                         // 不限制合约地址(后面会过滤)
			Topics: [][]common.Hash{
				{s.TransferTopic}, // Topic 0: Transfer 事件签名
				{},                // Topic 1: From (不限)
				{targetHash},      // Topic 2: To (目标钱包地址)
			},
		}

		var logs []types.Log
		var err error

		// ---------------------- 智能重试逻辑 ----------------------
		// 遇到网络错误时无限重试 (直到 Context 取消)，确保不漏块
		for {
			// 检查全局上下文是否取消 (如超时)
			if ctx.Err() != nil {
				break
			}

			// 获取当前客户端 (读锁)
			s.mu.RLock()
			currentClient := s.Client
			currentRPC := s.currentRPC
			s.mu.RUnlock()

			// 调用 RPC 接口查询日志
			logs, err = currentClient.FilterLogs(ctx, query)
			if err == nil {
				// 成功则跳出重试循环
				break
			}

			errMsg := err.Error()

			// 简化显示：如果是 HTML (通常是 429 返回的页面) 或太长，避免刷屏
			displayMsg := errMsg
			if strings.Contains(errMsg, "<html") || strings.Contains(errMsg, "<!doctype") {
				displayMsg = "HTTP Error (HTML body omitted)"
			} else if len(errMsg) > 80 {
				displayMsg = errMsg[:80] + "..."
			}

			fmt.Printf("⚠️  节点 %s 遇到错误: %s (Worker %d)\n", currentRPC, displayMsg, id)

			// 检查是否是 429 (请求过多) 或 limit exceeded 错误，或者超时/无响应/数据修剪(pruned)
			if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "limit") ||
				strings.Contains(errMsg, "deadline") || strings.Contains(errMsg, "timeout") ||
				strings.Contains(errMsg, "no response") || strings.Contains(errMsg, "pruned") {
				// -------------------------------------------------------------
				// 触发自动切换节点逻辑
				// -------------------------------------------------------------
				s.switchNode(currentRPC)

				// 稍微停顿一下等待切换完成
				time.Sleep(1 * time.Second)
			} else {
				// 普通网络错误，短暂等待
				time.Sleep(500 * time.Millisecond)
			}
		}
		// -----------------------------------------------------------

		// 如果是因为 Context 取消而退出的，直接返回
		if ctx.Err() != nil {
			return
		}

		// 2. 处理查询结果
		var blockRecords []DepositRecord
		for _, vLog := range logs {
			// 二次确认日志来自 USDT 合约地址 (防止假币)
			if vLog.Address == s.ContractAddress {
				// 解析日志数据
				rec, ok := s.parseLog(vLog)
				if ok {
					// -----------------------------------------------------
					// 获取区块时间信息
					// 注意：这里也要加锁获取 Client
					// -----------------------------------------------------
					s.mu.RLock()
					clientForHeader := s.Client
					s.mu.RUnlock()

					header, err := clientForHeader.HeaderByNumber(ctx, big.NewInt(int64(vLog.BlockNumber)))
					if err == nil {
						// 将秒级时间戳转为 Go 的 Time 对象
						rec.Time = time.Unix(int64(header.Time), 0)
					} else {
						// 如果获取时间失败，用当前时间暂代，确保不报错
						rec.Time = time.Now()
					}

					// 将记录加入当前区块的结果列表
					blockRecords = append(blockRecords, rec)
				}
			}
		}

		// 将当前区块的所有结果发送到结果通道
		results <- blockRecords
		// 稍微休眠，避免请求过于密集 (可根据节点情况调整)
		time.Sleep(50 * time.Millisecond)
	}
}

// parseLog 解析单个日志为 DepositRecord 结构
func (s *TokenScanner) parseLog(vLog types.Log) (DepositRecord, bool) {
	// 检查 topics 长度，标准的 Transfer 事件应该有 3 个 topic (签名, from, to)
	if len(vLog.Topics) < 3 {
		return DepositRecord{}, false
	}
	// 解析 Data 字段中的金额
	amountInt := new(big.Int).SetBytes(vLog.Data)

	// 组装并返回记录
	return DepositRecord{
		TxHash:      vLog.TxHash.Hex(),                                   // 交易 Hash
		BlockNumber: vLog.BlockNumber,                                    // 区块号
		From:        common.BytesToAddress(vLog.Topics[1].Bytes()).Hex(), // 发送方 (Topic 1)
		To:          common.BytesToAddress(vLog.Topics[2].Bytes()).Hex(), // 接收方 (Topic 2)
		Amount:      weiToDecimal(amountInt, TOKEN_DECIMALS),             // 转换金额精度
		LogIndex:    vLog.Index,                                          // 日志索引
	}, true
}

// weiToDecimal 将 wei (大整数) 转换为带小数点的 float (USDT 精度)
func weiToDecimal(ivalue *big.Int, decimals int) *big.Float {
	fvalue := new(big.Float).SetInt(ivalue)         // 转为 float
	floatDecimals := new(big.Float).SetFloat64(1.0) // 初始除数 1.0
	ten := new(big.Float).SetFloat64(10)            // 基数 10

	// 计算 10 的 decimals 次方
	for i := 0; i < decimals; i++ {
		floatDecimals.Mul(floatDecimals, ten)
	}
	// 执行除法
	return new(big.Float).Quo(fvalue, floatDecimals)
}
