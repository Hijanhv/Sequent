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

// buildVaultArtifact compiles the test Vault with Foundry and returns the path to
// its build artifact, skipping the test when forge is unavailable.
func buildVaultArtifact(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("forge"); err != nil {
		t.Skip("forge not installed; skipping integration test")
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "foundry.toml"), "[profile.default]\nsrc = \"src\"\nout = \"out\"\n")
	writeFile(t, filepath.Join(dir, "src", "Vault.sol"), vaultSource)

	if output, err := exec.Command("forge", "build", "--root", dir).CombinedOutput(); err != nil {
		t.Skipf("forge build failed (environment issue): %v\n%s", err, output)
	}
	return filepath.Join(dir, "out", "Vault.sol", "Vault.json")
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
