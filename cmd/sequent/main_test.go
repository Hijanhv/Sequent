package main

import (
	"bytes"
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
	if _, err := exec.LookPath("forge"); err != nil {
		t.Skip("forge not installed; skipping integration test")
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "foundry.toml"), "[profile.default]\nsrc = \"src\"\nout = \"out\"\n")
	writeFile(t, filepath.Join(dir, "src", "Vault.sol"), vaultSource)

	build := exec.Command("forge", "build", "--root", dir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Skipf("forge build failed (environment issue): %v\n%s", err, output)
	}

	artifact := filepath.Join(dir, "out", "Vault.sol", "Vault.json")
	var out, errBuf bytes.Buffer
	if code := run([]string{"analyze", artifact}, &out, &errBuf); code != 0 {
		t.Fatalf("run exit = %d, want 0 (stderr: %s)", code, errBuf.String())
	}

	// total is a plain slot both deposit and the getter touch, so this edge must
	// appear regardless of the mapping-key subtleties around balances.
	if !strings.Contains(out.String(), "deposit() -> total()") {
		t.Fatalf("expected a deposit() -> total() dependency, got:\n%s", out.String())
	}
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
