# Vault example

A minimal contract for trying Sequent end to end.

```sh
cd examples/vault
forge build
go run ../../cmd/sequent analyze out/Vault.sol/Vault.json
```

Add `--tests test` to also generate a Foundry reproduction test, then run
`forge test` to watch the ordering effects reproduce.
