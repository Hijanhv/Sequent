package analyze

import (
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/evm"
)

// ResolveCreationCode returns creation bytecode that actually deploys. A
// contract's constructor may take arguments, which a compiler appends to the
// creation bytecode at deploy time; without them the init code reverts. This
// tries argument sets from the same corpus the fuzzer uses and returns the
// bytecode with the first set that deploys, so the rest of the analysis, and the
// generated reproduction test, can deploy the contract the same way.
//
// The corpus leads with non-zero values, so a constructor guarded by something
// like require(x > 0) is handled too. If no argument set deploys, it returns an
// error describing the last failure.
func ResolveCreationCode(bytecode []byte, a abi.ABI, deployer common.Address) ([]byte, error) {
	candidates, err := constructorArgVariants(a, deployer)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, args := range candidates {
		code := concat(bytecode, args)
		e, err := evm.New()
		if err != nil {
			return nil, err
		}
		if _, err := e.Deploy(deployer, code); err != nil {
			lastErr = err
			continue
		}
		return code, nil
	}
	return nil, fmt.Errorf("contract does not deploy; the constructor reverted for every argument set tried: %w", lastErr)
}

// constructorArgVariants returns the candidate encoded constructor argument sets
// to try, or a single empty set when the constructor takes no arguments.
func constructorArgVariants(a abi.ABI, deployer common.Address) ([][]byte, error) {
	if len(a.Constructor.Inputs) == 0 {
		return [][]byte{nil}, nil
	}

	addrs := addressCorpus(deployer)
	seen := make(map[string]struct{})
	var variants [][]byte
	for r := 0; r < defaultRounds; r++ {
		args := corpusArgs(a.Constructor.Inputs, r, addrs)
		packed, err := a.Constructor.Inputs.Pack(args...)
		if err != nil {
			continue
		}
		if _, dup := seen[string(packed)]; dup {
			continue
		}
		seen[string(packed)] = struct{}{}
		variants = append(variants, packed)
	}
	if len(variants) == 0 {
		return nil, fmt.Errorf("cannot encode constructor arguments")
	}
	return variants, nil
}

func concat(bytecode, args []byte) []byte {
	out := make([]byte, 0, len(bytecode)+len(args))
	out = append(out, bytecode...)
	out = append(out, args...)
	return out
}
