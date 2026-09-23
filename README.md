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

## How it works

You hand Sequent a compiled contract. It runs the contract inside its own small
Ethereum, watches what each function does, works out which functions can affect
each other through shared storage, proves which of those effects are real, and
hands back a ranked list of findings plus a test that reproduces each one.

```
  input                                             output
  -----                                             ------
  a compiled contract              findings, ranked HIGH -> LOW, each with the
  (Foundry artifact, or            measured change and a runnable proof test
   ABI + bytecode files)
        |                                              ^
        v                                              |
  +------------------------------------------------------------+
  |                          sequent                           |
  |                                                            |
  |  1. LOAD     read the ABI (the list of functions) and      |
  |              the bytecode (the compiled code)              |
  |                              |                             |
  |                              v                             |
  |  2. RUN      deploy into an in-memory EVM and execute      |
  |              each function, recording every storage        |
  |              slot it reads and writes                      |
  |                              |                             |
  |                              v                             |
  |  3. CONNECT  draw  A -> B  whenever A writes a slot that   |
  |              B reads  (this is the interaction graph)      |
  |                              |                             |
  |                              v                             |
  |  4. FUZZ     re-run each function with several inputs, in  |
  |              parallel, so functions that key storage by    |
  |              an argument line up on the same key           |
  |                              |                             |
  |                              v                             |
  |  5. CONFIRM  for each A -> B, run B alone, then run A       |
  |              just before B, and compare B's result         |
  |                              |                             |
  |                              v                             |
  |  6. REPORT   rank by how serious the change is; print as   |
  |              text or JSON; generate a Foundry proof test   |
  +------------------------------------------------------------+
```

### The mechanism, step by step

1. **Load.** A contract describes itself with an ABI (its list of functions) and
   ships as compiled bytecode. Sequent reads both. It does not need the Solidity
   source.
2. **Run.** It deploys the bytecode into an EVM it carries inside itself, calls
   each function, and records the exact storage slots touched. Because the EVM is
   in memory and starts empty, the same contract always gives the same result. If
   the constructor needs arguments, Sequent tries values until the contract
   deploys, so contracts that take constructor parameters work too.
3. **Connect.** If `deposit` writes slot 7 and `balanceOf` reads slot 7, Sequent
   draws `deposit -> balanceOf`. Pairs that share no slot are dropped, so only
   the pairs where order could matter move forward.
4. **Fuzz.** Many functions behave differently depending on their arguments, and
   a `mapping` stores each key at a different slot. So Sequent calls each function
   several times with a small set of values, reusing the same caller address as a
   candidate, so a function that writes `balances[msg.sender]` and one that reads
   `balances[someAddress]` end up at the same slot and connect.
5. **Confirm.** A shared slot only means an effect is possible. To prove it,
   Sequent runs the reader by itself and then runs the writer just before it, and
   checks whether the reader's answer changed:

   ```
   starting point:   balances[you] = 0

   honest order:     balanceOf(you)              -> 0
   attacker first:   deposit(); balanceOf(you)   -> 1
                                                    ^
                     the answer moved from 0 to 1, so putting deposit
                     first provably changes what balanceOf reports
   ```

6. **Report.** Confirmed effects are ranked by how serious they are (moving a
   value the reader returns, or flipping it between reverting and succeeding,
   ranks highest), printed as text or JSON, and turned into a Foundry test that
   replays the two orderings so anyone can run the proof.

### Where each step lives in the code

| Step | Package |
| --- | --- |
| 1. Load | `internal/contract` |
| 2. Run (embedded EVM + tracer) | `internal/evm`, `internal/trace` |
| 3. Connect (interaction graph) | `internal/graph` |
| 4. Fuzz | `internal/analyze` (corpus, fuzz) |
| 5. Confirm | `internal/analyze` (scenario) |
| 6. Report + proof test | `internal/report`, `internal/gentest` |
| Command that ties it together | `cmd/sequent` |

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

A second loader takes separate ABI and bytecode files (`--abi` and `--bin`), the
shape most compilers can emit, for teams not on Foundry. It is a thin adapter
over the same core, which is exactly the point: the expensive work, parsing the
ABI and driving the contract, is written once and shared by every loader.

One more loader is planned: fetching bytecode from a live chain by address, which
turns Sequent into a scanner for already-deployed contracts. It too is just
another adapter over the same core, so the door is open.

Doing the artifact loader first got Sequent running on real code the fastest
without closing any of these off.

## Using it

### Prerequisites

- **Go 1.23 or newer** to build and run Sequent.
- **Foundry (`forge`)** to compile your contracts into artifacts, and to run the
  reproduction tests Sequent generates. Sequent itself needs no external tool at
  runtime once you have an artifact, or an ABI and bytecode.

### Install

```sh
git clone https://github.com/Hijanhv/Sequent.git
cd Sequent
go build -o sequent ./cmd/sequent
```

That produces a `sequent` binary. The examples below use `go run ./cmd/sequent`
so they work without building; substitute `./sequent` if you built the binary.

### Quick start

There is a ready-to-run example under `examples/vault`:

```sh
cd examples/vault && forge build
go run ../../cmd/sequent analyze out/Vault.sol/Vault.json
```

For your own contract, build it with Foundry and point Sequent at the artifact
(it lives under `out/<File>.sol/<Contract>.json`):

```sh
forge build
go run ./cmd/sequent analyze out/Vault.sol/Vault.json
```

Add `--json` for machine-readable output that another tool or a CI job can
consume:

```sh
go run ./cmd/sequent analyze --json out/Vault.sol/Vault.json
```

Add `--tests <dir>` to write a Foundry test that reproduces each confirmed
finding. Drop the generated file into your project and run it:

```sh
go run ./cmd/sequent analyze --tests test out/Vault.sol/Vault.json
forge test
```

Each generated test deploys the contract twice, runs the reader on its own and
again after the writer, and asserts the reader's result changed. It is proof an
auditor or a developer can run, and it doubles as a regression check once the
issue is fixed. The file depends only on Foundry's built-in cheatcodes, not on
forge-std, so it compiles in any Foundry project.

If you are not on Foundry, point Sequent at a separate ABI file and bytecode file
instead of an artifact:

```sh
go run ./cmd/sequent analyze --abi Vault.abi --bin Vault.bin
```

### Command reference

```
sequent analyze [flags] <foundry-artifact.json>
sequent analyze [flags] --abi <file> --bin <file>
```

| Flag | Meaning |
| --- | --- |
| `--json` | Print findings as JSON instead of text, for CI or other tools. |
| `--tests <dir>` | Write a Foundry reproduction test for the confirmed findings into `<dir>`. |
| `--abi <file>` | Path to the ABI JSON array (use with `--bin` instead of an artifact). |
| `--bin <file>` | Path to the creation bytecode hex (use with `--abi`). |

The command exits `0` on success, `1` on an analysis error (for example a
contract that cannot be deployed), and `2` on incorrect usage.

Sequent deploys the contract into its in-memory EVM, calls each function, and
prints the ordering dependencies it found:

```
Findings (most severe first):
  HIGH   deposit() -> balanceOf(address)   reader value 0 -> 1 (change +1)
  HIGH   deposit() -> balances(address)    reader value 0 -> 1 (change +1)
  HIGH   deposit() -> total()              reader value 0 -> 1 (change +1)
  HIGH   deposit() -> withdraw()           reverted -> succeeded

Summary: 4 high, 0 medium, 0 low (of 4 dependencies).
```

(`balances(address)` is the public getter Solidity generates for the `balances`
mapping, so it appears alongside the explicit `balanceOf`.)

Each line is a pair where the writer changes storage the reader depends on,
ranked by how serious the effect is:

- **HIGH** means ordering provably changes a value the reader returns, shown as
  the before and after with the numeric change, or flips the reader between
  reverting and succeeding. In the example, calling `deposit` first turns a
  `balanceOf` answer from 0 into 1, and turns a `withdraw` that used to fail into
  one that succeeds. These are proven ordering effects, not guesses.
- **MEDIUM** means ordering was confirmed to change the reader's output, but not
  in the clean numeric form above (for example a struct or a byte string).
- **LOW** means the two touch the same storage slot, so an effect is possible,
  but Sequent's inputs did not make the reader's answer change. It is a lead to
  look at by hand, not a confirmed finding.

### From "they share state" to "how much order moves the value"

Sharing a storage slot only says an effect is possible. To tell whether it is
real, and how big it is, Sequent replays the two functions in both orders. It
runs the reader on its own and records the answer, then runs the writer
immediately before the reader and records the answer again. If the two answers
differ, the writer really does move what the reader sees.

When the reader returns a single 32-byte word, which is the shape of a balance,
a price, a reserve, or a supply, Sequent reports the signed change between the
two orderings and ranks findings by its size. That size is the concrete measure
of the effect: how far an attacker can move the value the victim reads by placing
their transaction first. This filters real findings out of the noise, because a
large contract has many functions that touch the same slot without one being able
to meaningfully move the other.

A note on scope: this size is measured in the units the contract itself reports,
not converted into a single priced asset like ETH or a dollar figure. Pricing an
arbitrary token requires a live market and an external price source, which a
single-contract sandbox does not have. Putting a common-asset price on the
effect, for contracts where that is well defined, is a planned next step; this
stage gives the exact, honest magnitude that such a price would be built on.

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
  replaying each writer-then-reader pair, and measures the signed change in the
  reader's value so findings can be ranked.
- `internal/contract`: loads a compiled contract, from a Foundry build artifact
  or from separate ABI and bytecode files, behind a shared core.
- `internal/report`: ranks the dependencies into findings by severity and renders
  them as text or, with `--json`, as machine-readable output for CI.
- `internal/gentest`: generates a runnable Foundry test that reproduces each
  confirmed finding.
- `cmd/sequent`: the command-line tool. `sequent analyze <artifact.json>` prints
  the ordering dependencies for a contract, ranked by severity with the measured
  effect of each, and can emit JSON or reproduction tests.

Planned:

- A loader that fetches a deployed contract's bytecode from a live chain by
  address, so already-deployed contracts can be scanned.
- Pricing the measured effect in a common asset like ETH, for contracts where a
  market model makes that well defined, on top of the raw magnitude Sequent
  already reports.

## Development

Requires Go 1.23 or newer.

```sh
make check   # gofmt, go vet, and tests
make test    # tests only
make build   # compile all packages
```

## About the author

Sequent was designed and built by Janhavi Chavada, a Go and Rust developer
focused on distributed systems and blockchain infrastructure. This project is
hands-on work across EVM internals, static and dynamic program analysis, parallel
execution, and clean, thoroughly tested Go.

I am actively looking for a role as a **backend, blockchain, or full-stack
developer**, working primarily in **Go** or **Rust**, and I am available to start
immediately. I am based in **Bangalore, India**, and open to remote work.

- GitHub: [@Hijanhv](https://github.com/Hijanhv)

## License

MIT. See [LICENSE](LICENSE).
