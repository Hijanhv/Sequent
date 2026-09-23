# Sequent

An MEV exposure scanner for smart contracts.

Sequent finds places where a party who controls transaction order (a searcher or
a block builder) can influence a contract call and extract value from its users.
Instead of flagging generic patterns, it works from what the code actually does
to storage, then narrows analysis to the function pairs that can affect each
other.

## The idea

Ordering only matters when two functions touch the same state. If function `A`
writes a storage slot that function `B` reads, then placing `A` before `B`
changes what `B` sees. That is the seed of front-running, sandwiching, and
oracle manipulation.

Sequent builds an **interaction graph** from execution traces:

1. Trace every function and record the storage slots it reads and writes.
2. Add an edge `A -> B` whenever `A` writes a slot that `B` reads.
3. The edges are the only pairs worth analyzing for extractable value.
   Everything with no shared state is pruned.

This turns "fuzz everything" into "examine the handful of pairs that actually
interact," which is both faster and explainable.

## Design decisions

**Traces come from an embedded EVM, not an external node.** Sequent runs
go-ethereum's `core/vm` in-process and attaches its own tracer that records
every `SLOAD` and `SSTORE` with the exact slot key. The alternative was to drive
Foundry and `anvil` and parse `debug_traceTransaction` output over RPC. The
embedded approach was chosen for three reasons:

- **Determinism.** The same contract and inputs always produce the same trace,
  so findings are reproducible and reports are stable across runs. RPC against a
  live node introduces state and timing that Sequent cannot control.
- **Self-contained.** No external process to install, launch, or keep in sync.
  The scanner is a single Go binary.
- **Control over the trace.** Owning the tracer means Sequent records precisely
  the storage events it needs, at the slot level, without parsing a generic and
  verbose structured-log format.

The trade-off is more upfront engineering to deploy bytecode into an in-memory
`StateDB` and drive calls directly. That cost is paid once and buys a reliable
foundation for everything above it.

## Status

Sequent is under active development. What is built and tested today:

- `internal/trace`: the storage-access model (slots, reads, writes, per-function
  traces).
- `internal/graph`: the interaction-graph engine that derives ordering
  dependencies from traces.

Planned next:

- EVM execution layer that produces real storage traces from a compiled
  contract, backed by go-ethereum.
- Scenario evaluation that measures value at risk for each edge.
- A report with severity ranking and a reproducible test per finding.

## Development

Requires Go 1.23 or newer.

```sh
make check   # gofmt, go vet, and tests
make test    # tests only
make build   # compile all packages
```

## License

MIT. See [LICENSE](LICENSE).
