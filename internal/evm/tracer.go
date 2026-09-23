package evm

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"

	"github.com/Hijanhv/Sequent/internal/trace"
)

// The EVM exposes two storage opcodes. SLOAD reads a slot and SSTORE writes one.
// In both cases the slot key sits on top of the stack when the opcode is about
// to run, which is the moment the tracer is called.
const (
	opSLOAD  = byte(vm.SLOAD)
	opSSTORE = byte(vm.SSTORE)
)

// storageTracer records, in execution order, every storage read and write made
// by one target contract while the EVM runs. Accesses by any other address, for
// example a contract the target calls into, are ignored, because each contract
// owns a separate storage space.
type storageTracer struct {
	target   common.Address
	accesses []trace.Access
}

func newStorageTracer(target common.Address) *storageTracer {
	return &storageTracer{target: target}
}

// hooks returns the tracing callbacks to hand to the EVM. Only OnOpcode is set,
// since storage access is all this tracer cares about.
func (t *storageTracer) hooks() *tracing.Hooks {
	return &tracing.Hooks{OnOpcode: t.onOpcode}
}

func (t *storageTracer) onOpcode(_ uint64, op byte, _, _ uint64, scope tracing.OpContext, _ []byte, _ int, _ error) {
	if op != opSLOAD && op != opSSTORE {
		return
	}
	if scope.Address() != t.target {
		return
	}
	stack := scope.StackData()
	if len(stack) == 0 {
		return
	}

	key := stack[len(stack)-1] // slot key is on top of the stack for both opcodes
	access := trace.Access{Kind: trace.Read, Slot: trace.Slot(key.Bytes32())}
	if op == opSSTORE {
		access.Kind = trace.Write
	}
	t.accesses = append(t.accesses, access)
}
