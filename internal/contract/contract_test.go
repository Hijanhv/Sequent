package contract

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleArtifact = `{
  "abi": [
    {"type":"function","name":"deposit","stateMutability":"nonpayable","inputs":[{"name":"amount","type":"uint256"}],"outputs":[]},
    {"type":"function","name":"balance","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]}
  ],
  "bytecode": {"object": "0x6001600155"}
}`

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestFromFoundryArtifact(t *testing.T) {
	c, err := FromFoundryArtifact(writeTemp(t, "Vault.json", sampleArtifact))
	if err != nil {
		t.Fatalf("FromFoundryArtifact: %v", err)
	}

	if _, ok := c.ABI.Methods["deposit"]; !ok {
		t.Fatalf("expected deposit method in ABI, got %v", c.ABI.Methods)
	}
	if _, ok := c.ABI.Methods["balance"]; !ok {
		t.Fatalf("expected balance method in ABI, got %v", c.ABI.Methods)
	}

	want := []byte{0x60, 0x01, 0x60, 0x01, 0x55}
	if len(c.Bytecode) != len(want) {
		t.Fatalf("bytecode = %x, want %x", c.Bytecode, want)
	}
	for i := range want {
		if c.Bytecode[i] != want[i] {
			t.Fatalf("bytecode = %x, want %x", c.Bytecode, want)
		}
	}
}

func TestFromFoundryArtifactErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "empty bytecode", content: `{"abi":[],"bytecode":{"object":"0x"}}`},
		{name: "no abi field", content: `{"bytecode":{"object":"0x6001"}}`},
		{name: "malformed json", content: `{not json`},
		{name: "bad hex bytecode", content: `{"abi":[],"bytecode":{"object":"0xzz"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := FromFoundryArtifact(writeTemp(t, "T.json", tt.content)); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestFromFoundryArtifactMissingFile(t *testing.T) {
	if _, err := FromFoundryArtifact(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Fatal("expected an error for missing file, got nil")
	}
}

const sampleABIArray = `[
  {"type":"function","name":"deposit","stateMutability":"nonpayable","inputs":[{"name":"amount","type":"uint256"}],"outputs":[]},
  {"type":"function","name":"balance","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]}
]`

func TestFromFiles(t *testing.T) {
	abiPath := writeTemp(t, "Vault.abi", sampleABIArray)
	binPath := writeTemp(t, "Vault.bin", "0x6001600155\n") // trailing newline is tolerated

	c, err := FromFiles(abiPath, binPath)
	if err != nil {
		t.Fatalf("FromFiles: %v", err)
	}
	if _, ok := c.ABI.Methods["deposit"]; !ok {
		t.Fatalf("expected deposit method, got %v", c.ABI.Methods)
	}
	want := []byte{0x60, 0x01, 0x60, 0x01, 0x55}
	if len(c.Bytecode) != len(want) {
		t.Fatalf("bytecode = %x, want %x", c.Bytecode, want)
	}
}

func TestFromFilesNoPrefixBytecode(t *testing.T) {
	abiPath := writeTemp(t, "T.abi", sampleABIArray)
	binPath := writeTemp(t, "T.bin", "6001") // no 0x prefix

	c, err := FromFiles(abiPath, binPath)
	if err != nil {
		t.Fatalf("FromFiles: %v", err)
	}
	if len(c.Bytecode) != 2 {
		t.Fatalf("bytecode = %x, want 6001", c.Bytecode)
	}
}

func TestFromFilesErrors(t *testing.T) {
	good := writeTemp(t, "ok.abi", sampleABIArray)
	goodBin := writeTemp(t, "ok.bin", "0x6001")

	tests := []struct {
		name    string
		abiPath string
		binPath string
	}{
		{name: "missing abi", abiPath: filepath.Join(t.TempDir(), "nope.abi"), binPath: goodBin},
		{name: "missing bin", abiPath: good, binPath: filepath.Join(t.TempDir(), "nope.bin")},
		{name: "bad abi", abiPath: writeTemp(t, "bad.abi", "{not json"), binPath: goodBin},
		{name: "empty bin", abiPath: good, binPath: writeTemp(t, "empty.bin", "0x")},
		{name: "bad hex bin", abiPath: good, binPath: writeTemp(t, "badhex.bin", "0xzz")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := FromFiles(tt.abiPath, tt.binPath); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}
