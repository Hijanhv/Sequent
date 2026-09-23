package analyze

import (
	"fmt"
	"runtime"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/evm"
	"github.com/Hijanhv/Sequent/internal/trace"
)

const defaultRounds = 4

// FuzzConfig tunes a fuzzing run. Zero values select sensible defaults.
type FuzzConfig struct {
	Rounds  int // argument value sets tried per function
	Workers int // parallel EVMs; defaults to the number of CPUs
}

func (c FuzzConfig) rounds() int {
	if c.Rounds > 0 {
		return c.Rounds
	}
	return defaultRounds
}

func (c FuzzConfig) workers(jobs int) int {
	w := c.Workers
	if w <= 0 {
		w = runtime.GOMAXPROCS(0)
	}
	if w > jobs {
		w = jobs
	}
	if w < 1 {
		w = 1
	}
	return w
}

// Fuzz deploys the contract into several independent in-memory EVMs and, in
// parallel, calls each function with a spread of argument values drawn from the
// corpus. For each function it unions the storage touched across all its calls,
// giving the fullest footprint the corpus can reach, and returns one trace per
// function. Functions whose arguments cannot be encoded are reported as skipped.
//
// The result is deterministic: each function's footprint is computed by a single
// worker from the same baseline, and the returned traces are sorted by name, so
// the graph does not depend on how work was scheduled across goroutines.
func Fuzz(bytecode []byte, a abi.ABI, deployer, caller common.Address, cfg FuzzConfig) ([]trace.Function, []SkippedFunction, error) {
	jobs, skipped := buildJobs(a, cfg.rounds(), addressCorpus(caller))
	if len(jobs) == 0 {
		return nil, skipped, nil
	}

	// Validate deployment once up front for a clear, early error.
	probe, err := evm.New()
	if err != nil {
		return nil, skipped, err
	}
	if _, err := probe.Deploy(deployer, bytecode); err != nil {
		return nil, skipped, fmt.Errorf("deploy contract: %w", err)
	}

	results, err := runJobs(bytecode, deployer, caller, jobs, cfg.workers(len(jobs)))
	if err != nil {
		return nil, skipped, err
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results, skipped, nil
}

// fuzzJob is the work of exercising one function with all of its variant calls.
type fuzzJob struct {
	name     string
	variants [][]byte // calldata, one per argument value set, already deduplicated
}

// buildJobs turns an ABI into one job per function, generating the variant
// calldata for each. This runs single-threaded and deterministically, so the
// bytes handed to the workers never depend on scheduling.
func buildJobs(a abi.ABI, rounds int, addrs []common.Address) ([]fuzzJob, []SkippedFunction) {
	names := make([]string, 0, len(a.Methods))
	for name := range a.Methods {
		names = append(names, name)
	}
	sort.Strings(names)

	var jobs []fuzzJob
	var skipped []SkippedFunction
	for _, name := range names {
		method := a.Methods[name]

		seen := make(map[string]struct{})
		var variants [][]byte
		var lastErr error
		for r := 0; r < rounds; r++ {
			args := corpusArgs(method.Inputs, r, addrs)
			packed, err := method.Inputs.Pack(args...)
			if err != nil {
				lastErr = err
				continue
			}
			calldata := make([]byte, 0, len(method.ID)+len(packed))
			calldata = append(calldata, method.ID...)
			calldata = append(calldata, packed...)

			key := string(calldata)
			if _, dup := seen[key]; dup {
				continue // e.g. a no-argument function is identical every round
			}
			seen[key] = struct{}{}
			variants = append(variants, calldata)
		}

		if len(variants) == 0 {
			reason := "no encodable arguments"
			if lastErr != nil {
				reason = fmt.Sprintf("encode arguments: %v", lastErr)
			}
			skipped = append(skipped, SkippedFunction{Name: method.Sig, Reason: reason})
			continue
		}
		jobs = append(jobs, fuzzJob{name: method.Sig, variants: variants})
	}
	return jobs, skipped
}

// runJobs executes the jobs across a pool of workers, each with its own EVM and
// its own freshly deployed copy of the contract, so no state is shared between
// goroutines.
func runJobs(bytecode []byte, deployer, caller common.Address, jobs []fuzzJob, workers int) ([]trace.Function, error) {
	jobCh := make(chan fuzzJob, len(jobs))
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	resCh := make(chan trace.Function, len(jobs))
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
			for job := range jobCh {
				resCh <- runJob(e, caller, addr, job)
			}
		}()
	}

	wg.Wait()
	close(resCh)
	close(errCh)

	if err := <-errCh; err != nil {
		return nil, err
	}

	results := make([]trace.Function, 0, len(jobs))
	for f := range resCh {
		results = append(results, f)
	}
	return results, nil
}

// runJob calls a function once per variant, each from the same baseline, and
// unions the storage accesses. The executor is snapshotted before each call and
// rewound after, so variants and later jobs never see each other's writes.
func runJob(e *evm.Executor, caller, addr common.Address, job fuzzJob) trace.Function {
	var accesses []trace.Access
	for _, calldata := range job.variants {
		snap := e.Snapshot()
		res := e.Call(caller, addr, calldata)
		accesses = append(accesses, res.Accesses...)
		e.RevertToSnapshot(snap)
	}
	return trace.Function{Name: job.name, Accesses: accesses}
}
