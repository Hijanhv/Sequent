package analyze

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

const sampleABI = `[
  {"type":"function","name":"setNum","stateMutability":"nonpayable","inputs":[{"name":"x","type":"uint256"}],"outputs":[]},
  {"type":"function","name":"setAddr","stateMutability":"nonpayable","inputs":[{"name":"a","type":"address"}],"outputs":[]},
  {"type":"function","name":"setFlag","stateMutability":"nonpayable","inputs":[{"name":"b","type":"bool"}],"outputs":[]},
  {"type":"function","name":"setData","stateMutability":"nonpayable","inputs":[{"name":"d","type":"bytes"}],"outputs":[]},
  {"type":"function","name":"setName","stateMutability":"nonpayable","inputs":[{"name":"n","type":"string"}],"outputs":[]},
  {"type":"function","name":"setPair","stateMutability":"nonpayable","inputs":[{"name":"x","type":"uint256"},{"name":"y","type":"address"}],"outputs":[]},
  {"type":"function","name":"setTuple","stateMutability":"nonpayable","inputs":[{"name":"p","type":"tuple","components":[{"name":"a","type":"uint256"},{"name":"b","type":"address"}]}],"outputs":[]},
  {"type":"function","name":"total","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]}
]`

func parseABI(t *testing.T, s string) abi.ABI {
	t.Helper()
	a, err := abi.JSON(bytes.NewReader([]byte(s)))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}
	return a
}

func TestSpecsFromABIEncodesAllTypes(t *testing.T) {
	a := parseABI(t, sampleABI)

	specs, skipped := SpecsFromABI(a)
	if len(skipped) != 0 {
		t.Fatalf("expected nothing skipped, got %v", skipped)
	}
	if len(specs) != len(a.Methods) {
		t.Fatalf("got %d specs, want %d (one per method)", len(specs), len(a.Methods))
	}

	// Every spec must begin with the correct 4-byte selector for its function.
	byName := make(map[string]CallSpec, len(specs))
	for _, s := range specs {
		byName[s.Name] = s
	}
	for _, m := range a.Methods {
		spec, ok := byName[m.Sig]
		if !ok {
			t.Fatalf("no spec for %s", m.Sig)
		}
		if len(spec.Calldata) < 4 || !bytes.Equal(spec.Calldata[:4], m.ID) {
			t.Fatalf("%s: calldata selector = %x, want %x", m.Sig, spec.Calldata, m.ID)
		}
	}
}

func TestSpecsFromABIIsDeterministic(t *testing.T) {
	a := parseABI(t, sampleABI)
	first, _ := SpecsFromABI(a)
	second, _ := SpecsFromABI(a)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("SpecsFromABI is not deterministic")
	}
	for i := 1; i < len(first); i++ {
		if first[i-1].Name > first[i].Name {
			t.Fatalf("specs not sorted by name at %d: %q then %q", i, first[i-1].Name, first[i].Name)
		}
	}
}

func TestSpecsFromABIEmpty(t *testing.T) {
	specs, skipped := SpecsFromABI(parseABI(t, `[]`))
	if len(specs) != 0 || len(skipped) != 0 {
		t.Fatalf("empty ABI should yield nothing, got %d specs and %d skipped", len(specs), len(skipped))
	}
}
