package main

import (
	"fmt"
	"log"

	"github.com/wangegou/bsc-usdt-scanner/scanner"
)

func main() {
	addr := "0x5bd808Ab85C124f99080da5F864EDcB39950edE5"

	// 扫描 USDT
	StartScan(addr, "USDT")

	// 扫描 USDC
	StartScan(addr, "USDC")
}

func StartScan(addr string, symbol string) {
	fmt.Printf("\n========== 开始扫描 %s ==========\n", symbol)
	// 调用 scanner 包封装好的扫描函数
	records, err := scanner.StartScan(addr, symbol)
	if err != nil {
		log.Printf("⚠️ 扫描失败: %v", err)
		return
	}

	// =================================================================
	// 下面是业务层的打印逻辑，你可以随心所欲地修改
	// =================================================================

	// 示例：仅打印最近 1 条
	if len(records) > 0 {
		for _, rec := range records[0:1] {
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

	// 打印总结
	fmt.Printf("\n📊 %s 扫描完成: 发现 %d 笔入账\n", symbol, len(records))
}
