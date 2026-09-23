package analyze

import (
	"bytes"
	"fmt"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/evm"
	"github.com/Hijanhv/Sequent/internal/graph"
)

// Impact is the result of testing one dependency edge by actually reordering the
// two functions. Confirmed is true when running the writer before the reader
// changes what the reader returns, compared with running the reader alone. Before
// and After hold the reader's output in those two orderings, so a report can show
// the concrete change.
//
// A shared storage slot only means an effect is possible. Confirmed means the
// effect was observed: the writer really does move what the victim reads.
type Impact struct {
	Writer    string
	Reader    string
	Confirmed bool
	Before    []byte
	After     []byte
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

// evalEdge tries the writer and reader argument variants against each other and
// returns the first pairing where the writer changes the reader's output.
func evalEdge(e *evm.Executor, caller, addr common.Address, edge graph.Edge, variants map[string][][]byte) Impact {
	imp := Impact{Writer: edge.Writer, Reader: edge.Reader}

	for _, writerCall := range variants[edge.Writer] {
		for _, readerCall := range variants[edge.Reader] {
			before := readerOutput(e, caller, addr, readerCall)
			after := writerThenReaderOutput(e, caller, addr, writerCall, readerCall)
			if !bytes.Equal(before, after) {
				imp.Confirmed = true
				imp.Before = before
				imp.After = after
				return imp
			}
		}
	}
	return imp
}

// readerOutput runs the reader alone from a clean baseline.
func readerOutput(e *evm.Executor, caller, addr common.Address, readerCall []byte) []byte {
	snap := e.Snapshot()
	defer e.RevertToSnapshot(snap)
	return e.Call(caller, addr, readerCall).Output
}

// writerThenReaderOutput runs the writer and then the reader in one sequence, so
// the reader sees the writer's state changes, then rewinds both.
func writerThenReaderOutput(e *evm.Executor, caller, addr common.Address, writerCall, readerCall []byte) []byte {
	snap := e.Snapshot()
	defer e.RevertToSnapshot(snap)
	e.Call(caller, addr, writerCall)
	return e.Call(caller, addr, readerCall).Output
}
