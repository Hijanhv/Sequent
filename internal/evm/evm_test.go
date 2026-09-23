package evm

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/trace"
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

func TestCallRecordsStorageAccessesInOrder(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Runtime that writes 7 to slot 1, then reads slot 1 back:
	//   PUSH1 7; PUSH1 1; SSTORE   (slot 1 <- 7)
	//   PUSH1 1; SLOAD             (read slot 1)
	//   POP; STOP
	runtime := []byte{
		0x60, 0x07, 0x60, 0x01, 0x55,
		0x60, 0x01, 0x54,
		0x50, 0x00,
	}
	addr, err := e.Deploy(common.HexToAddress("0x1000"), packDeploy(t, runtime))
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	res := e.Call(common.HexToAddress("0xcafe"), addr, nil)
	if res.Err != nil {
		t.Fatalf("Call returned execution error: %v", res.Err)
	}

	want := []trace.Access{
		{Kind: trace.Write, Slot: trace.SlotFromUint64(1)},
		{Kind: trace.Read, Slot: trace.SlotFromUint64(1)},
	}
	if !reflect.DeepEqual(res.Accesses, want) {
		t.Fatalf("accesses = %v, want %v", res.Accesses, want)
	}
}

func TestCallWithNoCodeHasNoAccesses(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res := e.Call(common.HexToAddress("0xcafe"), common.HexToAddress("0xabcd"), nil)
	if res.Err != nil {
		t.Fatalf("unexpected error calling empty address: %v", res.Err)
	}
	if len(res.Accesses) != 0 {
		t.Fatalf("expected no accesses, got %v", res.Accesses)
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
