package account

import (
	"context"
	"fmt"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/params"
)

// SetEIP1559Gas 配置 auth 的 gas 字段，使用 EIP-1559 模型。
// 如果节点不支持 EIP-1559（BaseFee == nil），保持 auth 默认值不变。
func SetEIP1559Gas(ctx context.Context, client *ethclient.Client, auth *bind.TransactOpts) error {
	// 1. 拉 latest header（获取当前 BaseFee）
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("header by number: %w", err)
	}

	// 2. 检查 header.BaseFee == nil（兼容性 fallback）
	if header.BaseFee == nil {
		log.Println("warn: node does not support EIP-1559")
		return nil
	}

	// 3. 设 auth.GasTipCap = 1 Gwei
	// tipBigInt := big.NewInt(1e9) // 1 Gwei = 10^9 wei
	// 1e9是float64字面量，在NewInt()中会做隐式转换，如果不在int64范围内会出错
	tipBigInt := big.NewInt(params.GWei) // 用go-ethereum 自带的常量，更地道

	// 4. 设 auth.GasFeeCap = 2 × BaseFee + Tip
	// big.Int 算术运算（不能用 + - * 这些原生运算符！）
	result := new(big.Int).Mul(header.BaseFee, big.NewInt(2))
	result.Add(result, tipBigInt)
	auth.GasTipCap = tipBigInt
	auth.GasFeeCap = result

	// 临时打印
	fmt.Println("SetEIP1559Gas: BaseFee =", header.BaseFee, "Tip =", tipBigInt, "FeeCap =", result)

	return nil
}
