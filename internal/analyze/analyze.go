// Package analyze ties the EVM layer to the interaction graph. It runs each
// function of a deployed contract, records what storage the function touches,
// and turns those traces into the ordering graph that the rest of Sequent
// reasons about.
package analyze

import (
	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/evm"
	"github.com/Hijanhv/Sequent/internal/trace"
)

// CallSpec invokes one contract function. Name is a human-readable label used in
// reports. Calldata is the ABI-encoded input: the 4-byte selector followed by
// the encoded arguments.
type CallSpec struct {
	Name     string
	Calldata []byte
}

// Traces runs each spec against contract and returns one trace per spec, in the
// same order. Every spec runs from the same baseline state: the executor is
// snapshotted before each call and rewound after, so a trace reflects only what
// that one function touches and never the leftovers of the function before it.
func Traces(e *evm.Executor, caller, contract common.Address, specs []CallSpec) []trace.Function {
	fns := make([]trace.Function, 0, len(specs))
	for _, spec := range specs {
		snap := e.Snapshot()
		res := e.Call(caller, contract, spec.Calldata)
		fns = append(fns, trace.Function{Name: spec.Name, Accesses: res.Accesses})
		e.RevertToSnapshot(snap)
	}
	return fns
}
