package evm

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// packDeploy wraps runtime code in the standard constructor preamble that copies
// the runtime into memory and returns it, which is how a deployment stores code.
// It supports runtime shorter than 256 bytes, which is all the tests need.
func packDeploy(t *testing.T, runtime []byte) []byte {
	t.Helper()
	if len(runtime) >= 256 {
		t.Fatalf("packDeploy: runtime of %d bytes is too long for this helper", len(runtime))
	}
	n := byte(len(runtime))
	preamble := []byte{
		0x60, n, // PUSH1 len
		0x60, 0x0c, // PUSH1 12 (offset where runtime begins in this init code)
		0x60, 0x00, // PUSH1 0
		0x39,    // CODECOPY
		0x60, n, // PUSH1 len
		0x60, 0x00, // PUSH1 0
		0xf3, // RETURN
	}
	return append(preamble, runtime...)
}

func TestDeployStoresRuntimeCode(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Runtime that returns the value 42. The bytes themselves do not matter for
	// this test; what matters is that exactly they end up stored at the address.
	runtime := []byte{
		0x60, 0x2a, // PUSH1 42
		0x60, 0x00, // PUSH1 0
		0x52,       // MSTORE
		0x60, 0x20, // PUSH1 32
		0x60, 0x00, // PUSH1 0
		0xf3, // RETURN
	}

	addr, err := e.Deploy(common.HexToAddress("0x1000"), packDeploy(t, runtime))
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	got := e.Code(addr)
	if !bytes.Equal(got, runtime) {
		t.Fatalf("stored code = %x, want %x", got, runtime)
	}
}

func TestDeployRejectsFailingInitCode(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 0xfe is the designated INVALID opcode; deployment must fail cleanly.
	if _, err := e.Deploy(common.HexToAddress("0x2000"), []byte{0xfe}); err == nil {
		t.Fatal("expected an error deploying invalid init code, got nil")
	}
}

func TestCodeEmptyForUnknownAddress(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if code := e.Code(common.HexToAddress("0xdead")); len(code) != 0 {
		t.Fatalf("expected no code at unused address, got %x", code)
	}
}
