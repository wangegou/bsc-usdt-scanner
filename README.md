# BSC USDT Scanner (BSC USDT 扫块器)

一个高性能、高可靠的币安智能链 (BSC) USDT 入账扫块工具。

它可以自动寻找最佳的 RPC 节点，智能处理限流 (429) 和超时问题，通过无缝切换节点来保证任务不中断。使用并发工兵模式，高效扫描指定钱包地址近期的 USDT 转账记录。

## 核心特性

- **多币种支持**: 目前支持扫描 **USDT** 和 **USDC** (Binance-Peg)。
- **自动切换 RPC (Auto-Switch)**: 当遇到限流、超时、无响应或**数据修剪(Pruned)**错误时，自动切换到下一个可用节点，确保任务不中断。
- **并发扫描 (Concurrent Scanning)**: 默认使用 5 个并发工兵 (Workers) 并行获取日志，极大提升扫描速度。
- **零漏块保障 (Reliability)**: 采用无限重试机制，只有成功获取数据才会继续，确保**Absolutely No Data Loss**。
- **智能排序**: 返回的结果已按时间倒序排列（最新的交易在最前面），方便查看。
- **简单易用**: 封装为标准 Go Module，一行代码即可调用。

## 安装

```bash
go get github.com/wangegou/bsc-usdt-scanner
```

## 使用示例

```go
package main

import (
	"fmt"
	"log"

	"github.com/wangegou/bsc-usdt-scanner/scanner"
)

func main() {
	// 需要监控的钱包地址
	walletAddr := "0x5bd808Ab85C124f99080da5F864EDcB39950edE5"

	// 1. 扫描 USDT
	scanToken(walletAddr, "USDT")

	// 2. 扫描 USDC
	scanToken(walletAddr, "USDC")
}

func scanToken(addr string, symbol string) {
	fmt.Printf("开始扫描 %s ...\n", symbol)
	
	// StartScan 返回按时间倒序排列的切片 (最新的在前)
	records, err := scanner.StartScan(addr, symbol)
	if err != nil {
		log.Printf("❌ %s 扫描失败: %v", symbol, err)
		return
	}

	fmt.Printf("✅ %s 扫描完成! 发现 %d 笔入账\n", symbol, len(records))

	// 示例：仅打印最近 1 条入账记录
	if len(records) > 0 {
		rec := records[0]
		fmt.Println("\n========================================================")
		fmt.Printf("💰 发现一笔新的 %s 入账！\n", symbol)
		fmt.Println("--------------------------------------------------------")
		fmt.Printf("⏰ 时间:  %s\n", rec.Time.Format("2006-01-02 15:04:05"))
		fmt.Printf("💎 金额:  %.2f %s\n", rec.Amount, symbol)
		fmt.Printf("👤 来自:  %s\n", rec.From)
		fmt.Printf("📦 区块:  %d\n", rec.BlockNumber)
		fmt.Printf("🔗 详情:  https://bscscan.com/tx/%s\n", rec.TxHash)
		fmt.Println("========================================================")
	}
}
```

## 项目结构

- **scanner/**: 核心逻辑包
  - `DefaultRPCs`: 内置的公共 RPC 节点列表。
  - `StartScan`: 封装好的扫描入口函数。
- **main.go**: 调用示例文件。

## 贡献

欢迎提交 Issue 或 Pull Request 来改进这个扫块器！
