// Package evm runs contract bytecode inside an in-memory Ethereum Virtual
// Machine. Nothing here talks to a network or an external process: state lives
// entirely in memory and starts empty every time. That is what lets Sequent
// produce the same trace for the same inputs on every run.
//
// This file is stage one of the EVM layer: it stands up the machine and can
// deploy a contract into it. Recording what a contract does to storage, and
// driving individual functions, are added on top in later stages.
package evm

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"

	"github.com/Hijanhv/Sequent/internal/trace"
)

// gasCap is the gas made available to a deployment or call. It is large enough
// for any contract Sequent analyzes and, because execution is local and free,
// there is no reason to meter it tightly.
const gasCap uint64 = 100_000_000

// Executor is an in-memory EVM with its own fresh state.
type Executor struct {
	state       *state.StateDB
	chainConfig *params.ChainConfig
	blockCtx    vm.BlockContext
}

// New returns an Executor with empty state and every protocol upgrade enabled,
// so modern contract bytecode runs the same way it would on a current network.
func New() (*Executor, error) {
	statedb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		return nil, fmt.Errorf("create in-memory state: %w", err)
	}

	zeroRandom := common.Hash{}
	return &Executor{
		state:       statedb,
		chainConfig: params.MergedTestChainConfig,
		blockCtx: vm.BlockContext{
			CanTransfer: canTransfer,
			Transfer:    transfer,
			GetHash:     func(uint64) common.Hash { return common.Hash{} },
			BlockNumber: big.NewInt(0),
			Time:        0,
			Difficulty:  big.NewInt(0),
			GasLimit:    gasCap,
			BaseFee:     big.NewInt(0),
			Random:      &zeroRandom, // post-merge: PREVRANDAO must be non-nil
		},
	}, nil
}

// Deploy runs constructor (init) bytecode and returns the address of the
// resulting contract. Init code is what a Solidity compiler emits: it executes
// once and returns the runtime code that is stored at the address.
func (e *Executor) Deploy(deployer common.Address, initCode []byte) (common.Address, error) {
	e.fund(deployer)

	_, addr, _, err := e.newEVM(deployer, nil).Create(deployer, initCode, gasCap, uint256.NewInt(0))
	if err != nil {
		return common.Address{}, fmt.Errorf("deploy contract: %w", err)
	}
	return addr, nil
}

// CallResult holds the outcome of a traced call. Accesses lists the storage
// reads and writes the called contract made, in execution order. Err is the
// contract's own execution error such as a revert, not a failure of Sequent;
// callers decide whether a reverting call is interesting.
type CallResult struct {
	Output   []byte
	Accesses []trace.Access
	Err      error
}

// Call invokes to with the given input, recording every storage access the
// contract at to makes. State changes from the call persist in the Executor, so
// successive calls see each other's effects, which is exactly what ordering
// analysis depends on.
func (e *Executor) Call(from, to common.Address, input []byte) CallResult {
	e.fund(from)

	tracer := newStorageTracer(to)
	out, _, err := e.newEVM(from, tracer.hooks()).Call(from, to, input, gasCap, uint256.NewInt(0))
	return CallResult{Output: out, Accesses: tracer.accesses, Err: err}
}

// Code returns the runtime code stored at addr, or nil if nothing is there.
func (e *Executor) Code(addr common.Address) []byte {
	return e.state.GetCode(addr)
}

// Snapshot records the current state and returns an identifier that
// RevertToSnapshot can restore it to. This lets a caller run one function, look
// at what it did, and rewind to a clean baseline before running the next.
func (e *Executor) Snapshot() int {
	return e.state.Snapshot()
}

// RevertToSnapshot rewinds state to the point the given snapshot was taken.
func (e *Executor) RevertToSnapshot(id int) {
	e.state.RevertToSnapshot(id)
}

// newEVM builds an EVM bound to the current state with origin as the sender.
// A nil tracer means no tracing.
func (e *Executor) newEVM(origin common.Address, tracer *tracing.Hooks) *vm.EVM {
	evm := vm.NewEVM(e.blockCtx, e.state, e.chainConfig, vm.Config{NoBaseFee: true, Tracer: tracer})
	evm.SetTxContext(vm.TxContext{Origin: origin, GasPrice: new(big.Int)})
	return evm
}

// fund makes sure an account exists and holds enough balance to cover any value
// a later call might send. The amounts are irrelevant to storage analysis; this
// only stops calls from failing on a balance check.
func (e *Executor) fund(addr common.Address) {
	if !e.state.Exist(addr) {
		e.state.CreateAccount(addr)
	}
	e.state.AddBalance(addr, uint256.MustFromDecimal("1000000000000000000"), tracing.BalanceChangeUnspecified)
}

func canTransfer(db vm.StateDB, addr common.Address, amount *uint256.Int) bool {
	return db.GetBalance(addr).Cmp(amount) >= 0
}

func transfer(db vm.StateDB, sender, recipient common.Address, amount *uint256.Int) {
	db.SubBalance(sender, amount, tracing.BalanceChangeTransfer)
	db.AddBalance(recipient, amount, tracing.BalanceChangeTransfer)
}
