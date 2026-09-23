package analyze

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/evm"
)

const ctorRequireSource = `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

contract CtorRequire {
    uint256 public v;
    constructor(uint256 x) { require(x > 0, "x must be positive"); v = x; }
    function set(uint256 y) external { v = y; }
    function get() external view returns (uint256) { return v; }
}
`

func mustDeploy(t *testing.T, code []byte) {
	t.Helper()
	e, err := evm.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e.Deploy(common.HexToAddress("0x1000"), code); err != nil {
		t.Fatalf("resolved code did not deploy: %v", err)
	}
}

func TestResolveCreationCodeNoConstructor(t *testing.T) {
	bytecode, parsed := compileVault(t) // Vault has no constructor arguments
	deployer := common.HexToAddress("0x1000")

	code, err := ResolveCreationCode(bytecode, parsed, deployer)
	if err != nil {
		t.Fatalf("ResolveCreationCode: %v", err)
	}
	if len(code) != len(bytecode) {
		t.Fatalf("no-constructor code changed length: got %d, want %d", len(code), len(bytecode))
	}
	mustDeploy(t, code)
}

func TestResolveCreationCodeConstructorWithRequire(t *testing.T) {
	// A constructor that reverts on a zero argument. Resolution must find a
	// working argument (the corpus leads with 1) and append it.
	bytecode, parsed := compileContract(t, "CtorRequire", ctorRequireSource)
	deployer := common.HexToAddress("0x1000")

	// The bare creation bytecode must not deploy on its own.
	e, err := evm.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e.Deploy(deployer, bytecode); err == nil {
		t.Fatal("expected bare bytecode with required constructor args to fail")
	}

	code, err := ResolveCreationCode(bytecode, parsed, deployer)
	if err != nil {
		t.Fatalf("ResolveCreationCode: %v", err)
	}
	if len(code) <= len(bytecode) {
		t.Fatalf("expected constructor args appended, code len %d not greater than bytecode len %d", len(code), len(bytecode))
	}
	mustDeploy(t, code)
}

func TestConstructorArgVariantsNoInputs(t *testing.T) {
	// An ABI with no constructor yields a single empty candidate.
	variants, err := constructorArgVariants(parseABI(t, `[]`), common.Address{})
	if err != nil {
		t.Fatalf("constructorArgVariants: %v", err)
	}
	if len(variants) != 1 || variants[0] != nil {
		t.Fatalf("expected one nil variant, got %v", variants)
	}
}
