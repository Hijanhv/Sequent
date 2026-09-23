package analyze

import (
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/evm"
	"github.com/Hijanhv/Sequent/internal/graph"
	"github.com/Hijanhv/Sequent/internal/trace"
)

// packDeploy wraps runtime code in the constructor preamble that returns it.
// Supports runtime shorter than 256 bytes.
func packDeploy(t *testing.T, runtime []byte) []byte {
	t.Helper()
	if len(runtime) >= 256 {
		t.Fatalf("packDeploy: runtime of %d bytes is too long", len(runtime))
	}
	n := byte(len(runtime))
	preamble := []byte{
		0x60, n, 0x60, 0x0c, 0x60, 0x00, 0x39, 0x60, n, 0x60, 0x00, 0xf3,
	}
	return append(preamble, runtime...)
}

// branchingContract returns runtime that branches on the first calldata byte:
// a zero byte writes 7 to slot 1, any other value reads slot 1.
//
//	PUSH1 0; CALLDATALOAD; PUSH1 0xF8; SHR   -> first calldata byte
//	PUSH1 0x0F; JUMPI                        -> jump to read branch if nonzero
//	PUSH1 7; PUSH1 1; SSTORE; STOP           -> write branch (slot 1 <- 7)
//	JUMPDEST; PUSH1 1; SLOAD; POP; STOP      -> read branch (offset 0x0F)
func branchingContract() []byte {
	return []byte{
		0x60, 0x00, 0x35, 0x60, 0xF8, 0x1C,
		0x60, 0x0F, 0x57,
		0x60, 0x07, 0x60, 0x01, 0x55, 0x00,
		0x5B, 0x60, 0x01, 0x54, 0x50, 0x00,
	}
}

func TestTracesAndGraphEndToEnd(t *testing.T) {
	e, err := evm.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	deployer := common.HexToAddress("0x1000")
	caller := common.HexToAddress("0xcafe")
	contract, err := e.Deploy(deployer, packDeploy(t, branchingContract()))
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	specs := []CallSpec{
		{Name: "write()", Calldata: []byte{0x00}},
		{Name: "read()", Calldata: []byte{0x01}},
	}

	traces := Traces(e, caller, contract, specs)
	wantTraces := []trace.Function{
		{Name: "write()", Accesses: []trace.Access{{Kind: trace.Write, Slot: trace.SlotFromUint64(1)}}},
		{Name: "read()", Accesses: []trace.Access{{Kind: trace.Read, Slot: trace.SlotFromUint64(1)}}},
	}
	if !reflect.DeepEqual(traces, wantTraces) {
		t.Fatalf("traces = %v, want %v", traces, wantTraces)
	}

	g := graph.Build(traces)
	wantEdges := []graph.Edge{
		{Writer: "write()", Reader: "read()", Slots: []trace.Slot{trace.SlotFromUint64(1)}},
	}
	if !reflect.DeepEqual(g.Edges, wantEdges) {
		t.Fatalf("edges = %v, want %v", g.Edges, wantEdges)
	}
}

func TestTracesAreIsolatedBetweenCalls(t *testing.T) {
	e, err := evm.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	contract, err := e.Deploy(common.HexToAddress("0x1000"), packDeploy(t, branchingContract()))
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	specs := []CallSpec{
		{Name: "write()", Calldata: []byte{0x00}},
		{Name: "read()", Calldata: []byte{0x01}},
	}

	// Because each call is snapshotted and rewound, running the set twice must
	// produce identical traces. If state leaked between calls it would not.
	first := Traces(e, common.HexToAddress("0xcafe"), contract, specs)
	second := Traces(e, common.HexToAddress("0xcafe"), contract, specs)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("traces differ between runs:\n%v\n%v", first, second)
	}
}
