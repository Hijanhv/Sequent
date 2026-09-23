package analyze

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/contract"
	"github.com/Hijanhv/Sequent/internal/evm"
	"github.com/Hijanhv/Sequent/internal/graph"
)

func TestBuildJobsDedupesAndVaries(t *testing.T) {
	a := parseABI(t, `[
		{"type":"function","name":"ping","stateMutability":"nonpayable","inputs":[],"outputs":[]},
		{"type":"function","name":"setNum","stateMutability":"nonpayable","inputs":[{"name":"x","type":"uint256"}],"outputs":[]}
	]`)

	jobs, skipped := buildJobs(a, 4, addressCorpus(common.Address{}))
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped: %v", skipped)
	}

	byName := make(map[string]fuzzJob)
	for _, j := range jobs {
		byName[j.name] = j
	}
	if got := len(byName["ping()"].variants); got != 1 {
		t.Fatalf("no-arg function should dedupe to one variant, got %d", got)
	}
	if got := len(byName["setNum(uint256)"].variants); got < 2 {
		t.Fatalf("uint256 function should produce multiple variants, got %d", got)
	}

	again, _ := buildJobs(a, 4, addressCorpus(common.Address{}))
	if !reflect.DeepEqual(jobs, again) {
		t.Fatal("buildJobs is not deterministic")
	}
}

func TestFuzzOnRealContract(t *testing.T) {
	if _, err := exec.LookPath("forge"); err != nil {
		t.Skip("forge not installed; skipping integration test")
	}
	bytecode, parsed := compileVault(t)

	deployer := common.HexToAddress("0x00000000000000000000000000000000000d0000")
	caller := common.HexToAddress("0x000000000000000000000000000000000000cafe")

	fns, skipped, err := Fuzz(bytecode, parsed, deployer, caller, FuzzConfig{})
	if err != nil {
		t.Fatalf("Fuzz: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped functions: %v", skipped)
	}
	fuzzGraph := graph.Build(fns)

	// The fuzzing win: deposit() writes balances[msg.sender], and calling
	// balanceOf with the caller address reads that same mapping slot, so the two
	// now connect.
	if !edgeExists(fuzzGraph, "deposit()", "balanceOf(address)") {
		t.Fatalf("expected deposit() -> balanceOf(address) with fuzzing; edges: %v", fuzzGraph.Edges)
	}

	// Zero-value analysis reads balances[0], a different slot, so it must miss
	// that edge. This is the concrete improvement fuzzing provides.
	e, err := evm.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	addr, err := e.Deploy(deployer, bytecode)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	specs, _ := SpecsFromABI(parsed)
	zeroGraph := graph.Build(Traces(e, caller, addr, specs))
	if edgeExists(zeroGraph, "deposit()", "balanceOf(address)") {
		t.Fatal("zero-value analysis unexpectedly found the mapping edge")
	}

	// Determinism and parallel safety: the graph must not depend on worker count.
	single, _, err := Fuzz(bytecode, parsed, deployer, caller, FuzzConfig{Workers: 1})
	if err != nil {
		t.Fatalf("Fuzz single worker: %v", err)
	}
	many, _, err := Fuzz(bytecode, parsed, deployer, caller, FuzzConfig{Workers: 8})
	if err != nil {
		t.Fatalf("Fuzz many workers: %v", err)
	}
	if !reflect.DeepEqual(graph.Build(single), graph.Build(many)) {
		t.Fatal("graph differs between 1 and 8 workers")
	}
}

func edgeExists(g graph.Graph, writer, reader string) bool {
	for _, e := range g.Edges {
		if e.Writer == writer && e.Reader == reader {
			return true
		}
	}
	return false
}

// compileVault builds the test Vault with Foundry and returns its bytecode and
// parsed ABI. It fails the test on a genuine build error.
func compileVault(t *testing.T) ([]byte, abi.ABI) {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "foundry.toml"), "[profile.default]\nsrc = \"src\"\nout = \"out\"\n")
	writeFile(t, filepath.Join(dir, "src", "Vault.sol"), vaultSource)

	if out, err := exec.Command("forge", "build", "--root", dir).CombinedOutput(); err != nil {
		t.Skipf("forge build failed (environment issue): %v\n%s", err, out)
	}

	c, err := contract.FromFoundryArtifact(filepath.Join(dir, "out", "Vault.sol", "Vault.json"))
	if err != nil {
		t.Fatalf("load artifact: %v", err)
	}
	return c.Bytecode, c.ABI
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const vaultSource = `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

contract Vault {
    mapping(address => uint256) public balances;
    uint256 public total;

    function deposit() external { balances[msg.sender] += 1; total += 1; }
    function withdraw() external { balances[msg.sender] -= 1; total -= 1; }
    function balanceOf(address a) external view returns (uint256) { return balances[a]; }
}
`
