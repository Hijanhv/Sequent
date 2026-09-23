package analyze

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/evm"
	"github.com/Hijanhv/Sequent/internal/graph"
)

// Impact is the result of testing one dependency edge by actually reordering the
// two functions. Confirmed is true when running the writer before the reader
// changes what the reader does, compared with running the reader alone: either
// its returned bytes change, or it flips between reverting and succeeding.
//
// Before and After hold the reader's output in the two orderings, and the
// Reverted flags say whether each ordering reverted. Delta is the signed change
// in the reader's value when both orderings succeed and return a single 32-byte
// word, which is the common shape of a balance, price, reserve, or supply query;
// it is nil otherwise. Delta is how much ordering moves the value the victim
// reads, and it is what findings are ranked by.
//
// A shared storage slot only means an effect is possible. Confirmed means the
// effect was observed: the writer really does move what the victim reads.
type Impact struct {
	Writer         string
	Reader         string
	Confirmed      bool
	Before         []byte
	After          []byte
	BeforeReverted bool
	AfterReverted  bool
	Delta          *big.Int

	// WriterCall and ReaderCall are the exact calldata that produced the
	// confirmed effect, kept so a reproduction test can replay it. They are nil
	// when the edge is not confirmed.
	WriterCall []byte
	ReaderCall []byte
}

// Evaluate tests each edge by front-running: it runs the reader alone, then runs
// the writer immediately before the reader, and compares the reader's output. It
// reuses the same argument corpus as fuzzing so the two functions line up on
// shared keys, and it tries the writer and reader argument variants against each
// other, reporting the first pairing that produces a change.
//
// Edges are evaluated in parallel, each on its own EVM, and the results are
// sorted, so the outcome does not depend on scheduling.
func Evaluate(bytecode []byte, a abi.ABI, deployer, caller common.Address, edges []graph.Edge, cfg FuzzConfig) ([]Impact, error) {
	if len(edges) == 0 {
		return nil, nil
	}

	jobs, _ := buildJobs(a, cfg.rounds(), addressCorpus(caller))
	variants := make(map[string][][]byte, len(jobs))
	for _, j := range jobs {
		variants[j.name] = j.variants
	}

	probe, err := evm.New()
	if err != nil {
		return nil, err
	}
	if _, err := probe.Deploy(deployer, bytecode); err != nil {
		return nil, fmt.Errorf("deploy contract: %w", err)
	}

	results, err := runEdges(bytecode, deployer, caller, edges, variants, cfg.workers(len(edges)))
	if err != nil {
		return nil, err
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Writer != results[j].Writer {
			return results[i].Writer < results[j].Writer
		}
		return results[i].Reader < results[j].Reader
	})
	return results, nil
}

func runEdges(bytecode []byte, deployer, caller common.Address, edges []graph.Edge, variants map[string][][]byte, workers int) ([]Impact, error) {
	edgeCh := make(chan graph.Edge, len(edges))
	for _, e := range edges {
		edgeCh <- e
	}
	close(edgeCh)

	resCh := make(chan Impact, len(edges))
	errCh := make(chan error, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			e, err := evm.New()
			if err != nil {
				errCh <- err
				return
			}
			addr, err := e.Deploy(deployer, bytecode)
			if err != nil {
				errCh <- err
				return
			}
			for edge := range edgeCh {
				resCh <- evalEdge(e, caller, addr, edge, variants)
			}
		}()
	}

	wg.Wait()
	close(resCh)
	close(errCh)

	if err := <-errCh; err != nil {
		return nil, err
	}

	results := make([]Impact, 0, len(edges))
	for imp := range resCh {
		results = append(results, imp)
	}
	return results, nil
}

// callOutcome is a reader call's observable result: what it returned and whether
// it reverted.
type callOutcome struct {
	output   []byte
	reverted bool
}

// evalEdge tries the writer and reader argument variants against each other and
// returns the first pairing where the writer changes what the reader does.
func evalEdge(e *evm.Executor, caller, addr common.Address, edge graph.Edge, variants map[string][][]byte) Impact {
	imp := Impact{Writer: edge.Writer, Reader: edge.Reader}

	for _, writerCall := range variants[edge.Writer] {
		for _, readerCall := range variants[edge.Reader] {
			before := readerAlone(e, caller, addr, readerCall)
			after := writerThenReader(e, caller, addr, writerCall, readerCall)
			if changed(before, after) {
				imp.Confirmed = true
				imp.Before, imp.BeforeReverted = before.output, before.reverted
				imp.After, imp.AfterReverted = after.output, after.reverted
				imp.Delta = valueDelta(before, after)
				imp.WriterCall = writerCall
				imp.ReaderCall = readerCall
				return imp
			}
		}
	}
	return imp
}

// changed reports whether the reader behaved differently across the two
// orderings, either in what it returned or in whether it reverted.
func changed(a, b callOutcome) bool {
	return a.reverted != b.reverted || !bytes.Equal(a.output, b.output)
}

// valueDelta is the signed change in the reader's value, defined only when both
// orderings succeed and return a single 32-byte word.
func valueDelta(before, after callOutcome) *big.Int {
	if before.reverted || after.reverted {
		return nil
	}
	if len(before.output) != 32 || len(after.output) != 32 {
		return nil
	}
	b := new(big.Int).SetBytes(before.output)
	a := new(big.Int).SetBytes(after.output)
	return new(big.Int).Sub(a, b)
}

// readerAlone runs the reader by itself from a clean baseline.
func readerAlone(e *evm.Executor, caller, addr common.Address, readerCall []byte) callOutcome {
	snap := e.Snapshot()
	defer e.RevertToSnapshot(snap)
	res := e.Call(caller, addr, readerCall)
	return callOutcome{output: res.Output, reverted: res.Err != nil}
}

// writerThenReader runs the writer and then the reader in one sequence, so the
// reader sees the writer's state changes, then rewinds both.
func writerThenReader(e *evm.Executor, caller, addr common.Address, writerCall, readerCall []byte) callOutcome {
	snap := e.Snapshot()
	defer e.RevertToSnapshot(snap)
	e.Call(caller, addr, writerCall)
	res := e.Call(caller, addr, readerCall)
	return callOutcome{output: res.Output, reverted: res.Err != nil}
}
