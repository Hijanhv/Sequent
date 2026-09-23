# Sequent

An MEV exposure scanner for smart contracts.

## What problem does this solve?

On a blockchain, transactions do not always run in the order people sent them.
Whoever assembles a block gets to choose the order, and they can also insert
their own transactions. If they put their transaction in front of yours, they
can change the situation your transaction sees and profit from the difference.
This is called MEV (Maximal Extractable Value). A common example is a
"sandwich": someone buys a token right before your large buy pushes the price
up, then sells right after, pocketing the gap. The cost lands on ordinary users.

Sequent looks at a smart contract and points out where this kind of reordering
can hurt its users, so the team can fix it before an attacker finds it.

## The core idea, in plain terms

A smart contract keeps its data in numbered storage boxes called slots. Every
function it exposes does some mix of reading from and writing to those boxes.

Ordering can only matter when two functions share a box:

- If function `A` **writes** box 5, and function `B` **reads** box 5, then
  running `A` before `B` changes what `B` sees.
- If two functions never touch the same box, their order relative to each other
  cannot matter at all.

So Sequent builds an **interaction graph**:

1. Run each function and write down which boxes it reads and which it writes.
2. Draw an arrow `A -> B` whenever `A` writes a box that `B` reads.
3. Only the pairs with an arrow are worth studying for MEV. Everything else is
   ignored.

This is the important trick. Instead of blindly testing every combination of
functions, Sequent first figures out which pairs can even affect each other, and
looks only at those. It is faster, and it is easy to explain why a pair was
flagged.

## Design decisions

### Why we bring our own EVM instead of using an outside one

To see which boxes a function touches, Sequent has to actually run the
contract's code. The EVM (Ethereum Virtual Machine) is the small computer that
runs that code. There were two ways to get one:

1. **Use an outside tool.** Start a separate program (Foundry's `anvil`), send
   the contract to it, and ask it afterwards for a log of what happened.
2. **Bring our own.** Include go-ethereum's EVM directly inside Sequent and
   watch the code run from the inside.

Sequent takes the second path. Here is why, and why the first was dropped:

- **Same answer every time.** With our own EVM, the same contract and the same
  inputs always produce the same result, so a finding can be trusted and
  reproduced. An outside program has its own changing state and timing, so the
  answer can drift between runs.
- **Nothing extra to install or manage.** Sequent stays a single program. With
  the outside approach, a user would have to install Foundry, keep it running,
  and keep its version in step with ours.
- **We see exactly what we need.** Running the EVM ourselves lets us watch each
  read and write of a storage box as it happens. The outside approach only hands
  back a large, general-purpose log that we would then have to pick apart.

The cost of our choice is more work up front to set up an in-memory EVM and feed
contracts into it. That work is done once, and everything else is built on top
of it.

### Why we build in small, verified stages

The EVM layer is the trickiest part of the project, so it is built one small
step at a time, and each step has a test that proves it works before the next
step starts. Think of building a wall one brick at a time and checking each
brick is level before laying the next, instead of stacking the whole wall and
hoping it stands.

Concretely, the EVM layer is built in three stages:

1. Put a contract into an in-memory EVM and confirm its code is really there.
2. Watch the code run and record every storage read and write.
3. Call each function in turn, collect its reads and writes, and feed them into
   the interaction graph.

Each stage lands with passing tests before the next begins. This keeps bugs from
piling up and hiding behind each other, and it means the project is always in a
working state.

### Why we read a Foundry artifact first

To analyze a contract, Sequent needs two things: its ABI (the list of functions)
and its bytecode (the compiled code to run). Those can come from different
places, so the analyzer is kept behind a small loader and never learns where a
contract came from. It only ever receives an ABI and bytecode.

The first loader reads a Foundry build artifact. When you compile with Foundry,
which is the toolchain most Web3 teams and auditors already use, it writes one
JSON file per contract that holds both the ABI and the bytecode together. Reading
that single file is the lowest-friction way to point Sequent at real code, and it
speaks the same language the target audience already works in.

Two other loaders were considered and deliberately left for later:

- **Separate ABI and bytecode files.** More toolchain-agnostic, but the user has
  to gather and pass two files. Because it is just another thin loader over the
  same shape, adding it later costs almost nothing.
- **Fetching bytecode from a live chain by address.** This is what turns Sequent
  into a scanner for already-deployed contracts, and it too is just another
  loader.

Doing the artifact loader first gets Sequent running on real code the fastest
without closing any of these doors. The expensive work, parsing the ABI and
driving the contract, is written once and shared by every loader.

## Using it

Build a contract with Foundry, then point Sequent at the artifact:

```sh
forge build
go run ./cmd/sequent analyze out/Vault.sol/Vault.json
```

Sequent deploys the contract into its in-memory EVM, calls each function, and
prints the ordering dependencies it found:

```
Ordering dependencies (Writer -> Reader):
  [confirmed] deposit() -> balanceOf(address)   reader output 0 -> 1
  [confirmed] deposit() -> total()              reader output 0 -> 1
  [confirmed] deposit() -> withdraw()           reader output 0x4e487b71... -> (no output)

4 dependencies found: 4 confirmed to change the reader's output, 0 sharing state only.
```

Each line is a pair where the writer changes storage the reader depends on. The
tag says how sure Sequent is:

- **[confirmed]** means Sequent actually ran the writer just before the reader
  and watched the reader's answer change. The `reader output` values are the
  before and after. In the example, calling `deposit` first turns a `balanceOf`
  answer from 0 into 1, and turns a `withdraw` that used to fail into one that
  succeeds. This is a proven ordering effect, not a guess.
- **[shared]** means the two touch the same storage slot, so an effect is
  possible, but Sequent's inputs did not manage to make the reader's answer
  change. It is a lead to look at by hand, not a confirmed finding.

### From "they share state" to "order provably changes the outcome"

Sharing a storage slot only says an effect is possible. To tell whether it is
real, Sequent replays the two functions in both orders. It runs the reader on its
own and records the answer, then runs the writer immediately before the reader
and records the answer again. If the two answers differ, the writer really does
move what the reader sees, and the dependency is marked confirmed with the exact
before and after values.

This is what separates a genuine finding from noise. A large contract has many
functions that happen to touch the same slot without one being able to
meaningfully influence the other; confirmation filters those out and shows, in
concrete numbers, the ones where transaction order changes the result.

### How arguments are chosen

A function's behavior, and the storage it touches, usually depends on its
arguments. Sequent calls each function several times with values drawn from a
small fixed set: a few numbers, a few addresses, true and false, some bytes. It
then unions the storage touched across those calls into one footprint per
function.

The set of candidate values is shared across functions on purpose. This is what
lets a function that writes a `mapping` keyed by `msg.sender` line up with one
that reads the same mapping through an address argument: the caller's address is
one of the candidate values, so both end up at the same storage slot and the
dependency between them is found. Calling with only zero values would miss it,
because the two would touch different slots.

The candidate set is deliberately small and fixed rather than random, so every
run is reproducible. It is a practical middle ground, not exhaustive: a function
reachable only with a very specific input may still be under-explored, and a
function whose arguments cannot be encoded is reported as skipped rather than
guessed at. Deeper input exploration is future work.

## Status

Sequent is under active development. Built and tested today:

- `internal/trace`: the storage model. A slot is a numbered box; a trace is the
  ordered list of boxes a function read and wrote.
- `internal/graph`: the interaction-graph engine that turns those traces into
  the `A -> B` ordering arrows described above.

- `internal/evm`: the embedded EVM. It deploys a contract into in-memory state,
  runs it, and records every storage read and write with the exact slot. All
  three stages above are done and tested.
- `internal/analyze`: turns a contract's ABI into calls, drives each function
  from a clean baseline, and feeds the traces into the graph. It calls each
  function with several argument value sets, in parallel across independent EVMs,
  and unions the storage each function touches. It also confirms dependencies by
  replaying each writer-then-reader pair and comparing the reader's output.
- `internal/contract`: loads a compiled contract. The first loader reads a
  Foundry build artifact.
- `cmd/sequent`: the command-line tool. `sequent analyze <artifact.json>` prints
  the ordering dependencies for a contract.

Planned:

- More loaders: separate ABI and bytecode files, and fetching a deployed
  contract's bytecode from a live chain by address.
- Putting a number on each confirmed dependency: how much value an attacker
  could extract, priced in a common asset, on top of the output change Sequent
  already shows.
- A report that ranks findings by severity and ships a runnable test for each.

## Development

Requires Go 1.23 or newer.

```sh
make check   # gofmt, go vet, and tests
make test    # tests only
make build   # compile all packages
```

## License

MIT. See [LICENSE](LICENSE).
