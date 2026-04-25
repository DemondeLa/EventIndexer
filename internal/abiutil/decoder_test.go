package abiutil

import (
	winnerpkg "EventIndexer/abigen/winner"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

type mockRevertErr struct {
	msg  string
	data string
}

func (e *mockRevertErr) Error() string {
	return e.msg
}

func (e *mockRevertErr) ErrorData() interface{} {
	return e.data
}

type mockRevertErrMap struct {
	msg  string
	data string
}

func (e *mockRevertErrMap) Error() string { return e.msg }
func (e *mockRevertErrMap) ErrorData() interface{} {
	return map[string]interface{}{
		"data":    e.data,
		"message": e.msg,
	}
}

// 断言 *mockRevertErr 类型能转成 dataError 接口
var _ dataError = (*mockRevertErr)(nil) // ← 编译期检查
var _ error = (*mockRevertErr)(nil)     // ← 顺便检查 error 接口

func newTestDecoder(t *testing.T) *Decoder {
	t.Helper() // 让测试失败时报错位置指向调用者，不是这个 helper
	decoder, err := NewDecoder(winnerpkg.WinnerTakesAllMetaData.ABI)
	if err != nil {
		t.Fatalf("NewDecoder failed: %v", err)
	}
	return decoder
}

func TestDecode_Nil(t *testing.T) {
	decoder := newTestDecoder(t)
	got := decoder.Decode(nil)
	want := ""
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecode_PlainError(t *testing.T) {
	decoder := newTestDecoder(t)
	err := errors.New("test plain error") // 标准库的普通 error，没有 ErrorData 方法
	got := decoder.Decode(err)
	want := "test plain error"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecode_KnownSelector(t *testing.T) {
	decoder := newTestDecoder(t)

	dataErr := hexutil.Encode(crypto.Keccak256([]byte("SubmitPhaseEnded()"))[:4])
	// hexutil.Encode 自动加 0x 前缀
	err := &mockRevertErr{data: dataErr}

	got := decoder.Decode(err)
	want := "custom error SubmitPhaseEnded[]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecode_UnknownSelector(t *testing.T) {
	decoder := newTestDecoder(t)

	err := &mockRevertErr{
		msg:  "execution reverted", // 模拟真实 revert 错误的消息
		data: "0xdeadbeef",
	}
	got := decoder.Decode(err)

	want := "unknown error (selector=0xdeadbeef): execution reverted"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecode_KnownSelector_MapForm(t *testing.T) {
	decoder := newTestDecoder(t)
	dataErr := hexutil.Encode(crypto.Keccak256([]byte("SubmitPhaseEnded()"))[:4])
	err := &mockRevertErrMap{
		msg:  "execution reverted", // 模拟真实 revert 错误的消息
		data: dataErr,
	}
	got := decoder.Decode(err)

	want := "custom error SubmitPhaseEnded[]"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
