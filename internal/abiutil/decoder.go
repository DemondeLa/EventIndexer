// Package abiutil provides utilities for decoding Ethereum contract ABI data,
// in particular custom errors returned by reverted transactions.
package abiutil

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Decoder decodes Ethereum execution errors into human-readable strings,
// using a contract's ABI to resolve custom error selectors to their names.
type Decoder struct {
	parsedABI abi.ABI
}

// NewDecoder parses the given ABI JSON and returns a Decoder ready to decode
// errors. It returns an error if the ABI JSON is invalid.
func NewDecoder(abiJSON string) (*Decoder, error) {
	// 用 abi.JSON 解析字符串，存进 parsedABI
	parsed, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		return nil, err
	}
	return &Decoder{parsedABI: parsed}, nil
}

// dataError is an interface implemented by errors that carry additional
// data alongside their message, such as the revert data returned by
// go-ethereum's RPC errors.
type dataError interface {
	ErrorData() interface{}
}

// extractRevertData tries to extract the revert data from the given error.
// It returns the data as a string and a boolean indicating whether the extraction was successful.
// The error must implement the dataError interface, and the data must be a string for the extraction to succeed.
func extractRevertData(err error) (string, bool) {
	de, ok := err.(dataError)
	if !ok {
		return "", false
	}

	raw := de.ErrorData()

	// case1: 直接返回string
	if data, ok := raw.(string); ok {
		return data, true
	}

	// case2: 返回map[string]interface{} 需要取data
	if mp, ok := raw.(map[string]interface{}); ok {
		if data, ok := mp["data"].(string); ok {
			return data, true
		}
	}

	return "", false
}

// Decode takes an error returned by a failed Ethereum transaction and attempts to decode it into a human-readable string.
func (d *Decoder) Decode(err error) string {
	if err == nil {
		return ""
	}
	msg, ok := extractRevertData(err)
	if !ok {
		return err.Error()
	}
	// msg是string, 还需要转成bytes
	dataBytes, decodeErr := hexutil.Decode(msg)
	if decodeErr != nil {
		return fmt.Sprintf("invalid hex in revert data: %s (original: %s)", decodeErr.Error(), err.Error())
	}

	if len(dataBytes) < 4 {
		return fmt.Sprintf("invalid revert data (got %d bytes): %s", len(dataBytes), err.Error())
	}

	var selector [4]byte
	copy(selector[:], dataBytes[:4])
	errorDef, lookupErr := d.parsedABI.ErrorByID(selector)
	if lookupErr != nil {
		return fmt.Sprintf("unknown error (selector=0x%x): %s", selector, err.Error())
	}

	args, unpackErr := errorDef.Unpack(dataBytes)
	if unpackErr != nil {
		return fmt.Sprintf("custom error %s (unpack failed: %v)", errorDef.Name, unpackErr)
	}
	return fmt.Sprintf("custom error %s%v", errorDef.Name, args)
	// %v 带[]
}
