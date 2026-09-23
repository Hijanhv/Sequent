package analyze

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/graph"
)

func TestEvaluateConfirmsMappingEffect(t *testing.T) {
	bytecode, parsed := compileVault(t) // skips if forge is absent

	deployer := common.HexToAddress("0x00000000000000000000000000000000000d0000")
	caller := common.HexToAddress("0x000000000000000000000000000000000000cafe")

	fns, _, err := Fuzz(bytecode, parsed, deployer, caller, FuzzConfig{})
	if err != nil {
		t.Fatalf("Fuzz: %v", err)
	}
	edges := graph.Build(fns).Edges

	impacts, err := Evaluate(bytecode, parsed, deployer, caller, edges, FuzzConfig{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	// deposit() increments balances[caller]; balanceOf(caller) reads it. Running
	// deposit first must change the reader output from 0 to 1.
	imp := findImpact(t, impacts, "deposit()", "balanceOf(address)")
	if !imp.Confirmed {
		t.Fatalf("expected deposit() -> balanceOf(address) to be confirmed")
	}
	if got := new(big.Int).SetBytes(imp.Before); got.Sign() != 0 {
		t.Fatalf("baseline balanceOf output = %s, want 0", got)
	}
	if got := new(big.Int).SetBytes(imp.After); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("front-run balanceOf output = %s, want 1", got)
	}
	if imp.Delta == nil || imp.Delta.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("expected value delta of +1, got %v", imp.Delta)
	}
}

func TestEvaluateDetectsRevertFlip(t *testing.T) {
	bytecode, parsed := compileVault(t)

	deployer := common.HexToAddress("0x00000000000000000000000000000000000d0000")
	caller := common.HexToAddress("0x000000000000000000000000000000000000cafe")

	fns, _, err := Fuzz(bytecode, parsed, deployer, caller, FuzzConfig{})
	if err != nil {
		t.Fatalf("Fuzz: %v", err)
	}
	edges := graph.Build(fns).Edges

	impacts, err := Evaluate(bytecode, parsed, deployer, caller, edges, FuzzConfig{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	// From a clean state withdraw() underflows and reverts. After deposit() gives
	// the caller a balance, withdraw() succeeds. Ordering flips the outcome.
	imp := findImpact(t, impacts, "deposit()", "withdraw()")
	if !imp.Confirmed {
		t.Fatal("expected deposit() -> withdraw() to be confirmed")
	}
	if !imp.BeforeReverted || imp.AfterReverted {
		t.Fatalf("expected reverted -> succeeded, got before=%v after=%v", imp.BeforeReverted, imp.AfterReverted)
	}
	if imp.Delta != nil {
		t.Fatalf("a revert flip should have no numeric delta, got %v", imp.Delta)
	}
}

func TestEvaluateIsWorkerCountIndependent(t *testing.T) {
	bytecode, parsed := compileVault(t)

	deployer := common.HexToAddress("0x00000000000000000000000000000000000d0000")
	caller := common.HexToAddress("0x000000000000000000000000000000000000cafe")

	fns, _, err := Fuzz(bytecode, parsed, deployer, caller, FuzzConfig{})
	if err != nil {
		t.Fatalf("Fuzz: %v", err)
	}
	edges := graph.Build(fns).Edges

	single, err := Evaluate(bytecode, parsed, deployer, caller, edges, FuzzConfig{Workers: 1})
	if err != nil {
		t.Fatalf("Evaluate single: %v", err)
	}
	many, err := Evaluate(bytecode, parsed, deployer, caller, edges, FuzzConfig{Workers: 8})
	if err != nil {
		t.Fatalf("Evaluate many: %v", err)
	}
	if !reflect.DeepEqual(single, many) {
		t.Fatal("Evaluate results differ between 1 and 8 workers")
	}
}

func TestEvaluateNoEdges(t *testing.T) {
	impacts, err := Evaluate(nil, parseABI(t, `[]`), common.Address{}, common.Address{}, nil, FuzzConfig{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(impacts) != 0 {
		t.Fatalf("expected no impacts, got %v", impacts)
	}
}

func findImpact(t *testing.T, impacts []Impact, writer, reader string) Impact {
	t.Helper()
	for _, imp := range impacts {
		if imp.Writer == writer && imp.Reader == reader {
			return imp
		}
	}
	t.Fatalf("no impact for %s -> %s in %v", writer, reader, impacts)
	return Impact{}
}
