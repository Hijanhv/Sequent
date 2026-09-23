package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunArgumentHandling(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "no args", args: nil, want: 2},
		{name: "unknown command", args: []string{"frobnicate"}, want: 2},
		{name: "analyze without path", args: []string{"analyze"}, want: 2},
		{name: "analyze too many args", args: []string{"analyze", "a", "b"}, want: 2},
		{name: "analyze missing file", args: []string{"analyze", filepath.Join(t.TempDir(), "nope.json")}, want: 1},
		{name: "abi without bin", args: []string{"analyze", "--abi", "a.abi"}, want: 2},
		{name: "bin without abi", args: []string{"analyze", "--bin", "b.bin"}, want: 2},
		{name: "files with artifact", args: []string{"analyze", "--abi", "a", "--bin", "b", "x.json"}, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			if got := run(tt.args, &out, &errBuf); got != tt.want {
				t.Fatalf("run(%v) = %d, want %d (stderr: %s)", tt.args, got, tt.want, errBuf.String())
			}
		})
	}
}

// TestAnalyzeRealContract compiles a contract with Foundry and runs the full CLI
// against the artifact. It is skipped when forge is unavailable so the rest of
// the suite stays self-contained.
func TestAnalyzeRealContract(t *testing.T) {
	artifact := buildVaultArtifact(t)

	var out, errBuf bytes.Buffer
	if code := run([]string{"analyze", artifact}, &out, &errBuf); code != 0 {
		t.Fatalf("run exit = %d, want 0 (stderr: %s)", code, errBuf.String())
	}

	// total is a plain slot both deposit and the getter touch.
	if !strings.Contains(out.String(), "deposit() -> total()") {
		t.Fatalf("expected a deposit() -> total() dependency, got:\n%s", out.String())
	}
	// Argument fuzzing calls balanceOf with the caller address, so it reads the
	// same mapping slot deposit writes. This edge is the fuzzing payoff.
	if !strings.Contains(out.String(), "deposit() -> balanceOf(address)") {
		t.Fatalf("expected a deposit() -> balanceOf(address) dependency, got:\n%s", out.String())
	}
	// Scenario evaluation quantifies the effect, so the mapping edge is a HIGH
	// finding with a numeric value change.
	if !strings.Contains(out.String(), "HIGH") {
		t.Fatalf("expected at least one HIGH finding, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "change +1") {
		t.Fatalf("expected a quantified value change, got:\n%s", out.String())
	}
}

// TestAnalyzeJSONOutput checks the machine-readable output parses and carries the
// expected findings.
func TestAnalyzeJSONOutput(t *testing.T) {
	artifact := buildVaultArtifact(t)

	var out, errBuf bytes.Buffer
	if code := run([]string{"analyze", "--json", artifact}, &out, &errBuf); code != 0 {
		t.Fatalf("run exit = %d, want 0 (stderr: %s)", code, errBuf.String())
	}

	var got struct {
		Findings []struct {
			Severity string `json:"severity"`
			Writer   string `json:"writer"`
			Reader   string `json:"reader"`
		} `json:"findings"`
		Summary struct {
			High int `json:"high"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if got.Summary.High < 1 {
		t.Fatalf("expected at least one HIGH finding in JSON, got %+v", got.Summary)
	}

	found := false
	for _, f := range got.Findings {
		if f.Writer == "deposit()" && f.Reader == "balanceOf(address)" && f.Severity == "HIGH" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a HIGH deposit() -> balanceOf(address) finding in JSON, got %+v", got.Findings)
	}
}

// TestGeneratedTestsPassInFoundry generates reproduction tests for the Vault and
// runs them with forge, proving the generated suite compiles and passes.
func TestGeneratedTestsPassInFoundry(t *testing.T) {
	dir, artifact := buildVaultProject(t)
	testDir := filepath.Join(dir, "test")

	var out, errBuf bytes.Buffer
	if code := run([]string{"analyze", "--tests", testDir, artifact}, &out, &errBuf); code != 0 {
		t.Fatalf("run exit = %d, want 0 (stderr: %s)", code, errBuf.String())
	}

	output, err := exec.Command("forge", "test", "--root", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("generated tests did not pass forge test: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "0 failed") {
		t.Fatalf("expected all generated tests to pass, got:\n%s", output)
	}
}

// TestAnalyzeFromSeparateFiles checks the --abi/--bin loader by splitting a
// Foundry artifact into an ABI file and a bytecode file and analyzing those.
func TestAnalyzeFromSeparateFiles(t *testing.T) {
	artifact := buildVaultArtifact(t)

	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var parsed struct {
		ABI      json.RawMessage `json:"abi"`
		Bytecode struct {
			Object string `json:"object"`
		} `json:"bytecode"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse artifact: %v", err)
	}

	dir := t.TempDir()
	abiPath := filepath.Join(dir, "Vault.abi")
	binPath := filepath.Join(dir, "Vault.bin")
	writeFile(t, abiPath, string(parsed.ABI))
	writeFile(t, binPath, parsed.Bytecode.Object)

	var out, errBuf bytes.Buffer
	if code := run([]string{"analyze", "--abi", abiPath, "--bin", binPath}, &out, &errBuf); code != 0 {
		t.Fatalf("run exit = %d, want 0 (stderr: %s)", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "deposit() -> balanceOf(address)") {
		t.Fatalf("expected the mapping dependency from separate files, got:\n%s", out.String())
	}
}

// TestGuardTestsFailThenPassAfterFix generates regression-guard tests, confirms
// they FAIL on the current (vulnerable) Vault, then rewrites the source with a
// fixed version and confirms the same guard tests now PASS. Because the guards
// deploy the live source, they track the fix without being regenerated.
func TestGuardTestsFailThenPassAfterFix(t *testing.T) {
	dir, artifact := buildVaultProject(t)
	testDir := filepath.Join(dir, "test")

	var out, errBuf bytes.Buffer
	if code := run([]string{"analyze", "--guard-tests", testDir, artifact}, &out, &errBuf); code != 0 {
		t.Fatalf("run exit = %d, want 0 (stderr: %s)", code, errBuf.String())
	}

	// On the vulnerable contract the guards must fail.
	output, err := exec.Command("forge", "test", "--root", dir).CombinedOutput()
	if err == nil {
		t.Fatalf("guard tests should fail on the vulnerable contract, but forge test passed:\n%s", output)
	}
	if !strings.Contains(string(output), "regression") {
		t.Fatalf("expected a regression assertion failure, got:\n%s", output)
	}

	// Fix the source: deposit and withdraw no longer touch shared state, keeping
	// the same function selectors so the guard calldata still resolves.
	writeFile(t, filepath.Join(dir, "src", "Vault.sol"), fixedVaultSource)

	output, err = exec.Command("forge", "test", "--root", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("guard tests should pass on the fixed contract, but forge test failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "0 failed") {
		t.Fatalf("expected all guard tests to pass after the fix, got:\n%s", output)
	}
}

const fixedVaultSource = `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

contract Vault {
    mapping(address => uint256) public balances;
    uint256 public total;

    function deposit() external {}
    function withdraw() external {}
    function balanceOf(address a) external view returns (uint256) { return balances[a]; }
}
`

// buildVaultProject compiles the test Vault with Foundry and returns the project
// directory and the path to its build artifact, skipping when forge is absent.
func buildVaultProject(t *testing.T) (dir, artifact string) {
	t.Helper()
	if _, err := exec.LookPath("forge"); err != nil {
		t.Skip("forge not installed; skipping integration test")
	}

	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "foundry.toml"), "[profile.default]\nsrc = \"src\"\nout = \"out\"\ntest = \"test\"\n")
	writeFile(t, filepath.Join(dir, "src", "Vault.sol"), vaultSource)

	if output, err := exec.Command("forge", "build", "--root", dir).CombinedOutput(); err != nil {
		t.Skipf("forge build failed (environment issue): %v\n%s", err, output)
	}
	return dir, filepath.Join(dir, "out", "Vault.sol", "Vault.json")
}

// buildVaultArtifact returns just the artifact path for tests that do not need
// the project directory.
func buildVaultArtifact(t *testing.T) string {
	t.Helper()
	_, artifact := buildVaultProject(t)
	return artifact
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
