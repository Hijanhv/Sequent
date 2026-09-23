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

## Status

Sequent is under active development. Built and tested today:

- `internal/trace`: the storage model. A slot is a numbered box; a trace is the
  ordered list of boxes a function read and wrote.
- `internal/graph`: the interaction-graph engine that turns those traces into
  the `A -> B` ordering arrows described above.

In progress:

- `internal/evm`: the embedded EVM that produces real traces from a compiled
  contract (being built in the three stages listed above).

Planned:

- Scenario evaluation that estimates how much value is at risk for each arrow.
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
