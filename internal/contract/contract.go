// Package contract loads a compiled contract into the form Sequent analyzes:
// its ABI (the list of functions) and its creation bytecode (the code that gets
// deployed). Where those come from is deliberately kept behind loaders. The
// analyzer never cares whether a contract came from a Foundry build, from a pair
// of hand-supplied files, or later from a live chain; it only needs the ABI and
// the bytecode. New sources are added as new loaders over this same shape,
// without touching anything downstream.
package contract

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// Contract is a compiled contract ready to analyze.
type Contract struct {
	ABI      abi.ABI
	Bytecode []byte // creation (constructor) bytecode
}

// foundryArtifact is the subset of a Foundry build artifact that Sequent reads.
// Foundry writes one such JSON file per contract under out/<File>.sol/<Name>.json.
type foundryArtifact struct {
	ABI      json.RawMessage `json:"abi"`
	Bytecode struct {
		Object string `json:"object"`
	} `json:"bytecode"`
}

// FromFoundryArtifact loads a contract from a Foundry build artifact. Foundry is
// the toolchain most Web3 teams and auditors already use, and its artifact bundles
// the ABI and the bytecode in a single file, so this is the lowest-friction way to
// point Sequent at real code.
func FromFoundryArtifact(path string) (*Contract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read artifact: %w", err)
	}

	var art foundryArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("parse artifact %s: %w", path, err)
	}
	if len(art.ABI) == 0 {
		return nil, fmt.Errorf("artifact %s has no abi field", path)
	}

	parsedABI, err := abi.JSON(bytes.NewReader(art.ABI))
	if err != nil {
		return nil, fmt.Errorf("parse abi in %s: %w", path, err)
	}

	code, err := decodeHex(art.Bytecode.Object)
	if err != nil {
		return nil, fmt.Errorf("decode bytecode in %s: %w", path, err)
	}
	if len(code) == 0 {
		return nil, fmt.Errorf("artifact %s has empty bytecode (interface or abstract contract?)", path)
	}

	return &Contract{ABI: parsedABI, Bytecode: code}, nil
}

func decodeHex(s string) ([]byte, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return nil, nil
	}
	return hex.DecodeString(s)
}
