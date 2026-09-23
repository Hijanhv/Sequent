package analyze

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// SkippedFunction records a function that could not be turned into a call, so a
// report can be honest about what was not analyzed rather than silently dropping
// it.
type SkippedFunction struct {
	Name   string
	Reason string
}

// SpecsFromABI builds one CallSpec per function in the ABI, in a stable order.
//
// Each argument is filled with its zero value (0 for numbers, the zero address,
// false, empty bytes, and so on). This is enough to reach and exercise many
// functions, but a function guarded by a require on its inputs may revert early,
// so its recorded footprint can be incomplete. Driving functions with meaningful
// and fuzzed inputs is a later stage; until then the zero-value calls give a
// sound first pass and anything that cannot be encoded is returned as skipped
// rather than guessed at.
func SpecsFromABI(a abi.ABI) (specs []CallSpec, skipped []SkippedFunction) {
	names := make([]string, 0, len(a.Methods))
	for name := range a.Methods {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		method := a.Methods[name]

		args, err := zeroArgs(method.Inputs)
		if err != nil {
			skipped = append(skipped, SkippedFunction{Name: method.Sig, Reason: err.Error()})
			continue
		}
		packed, err := method.Inputs.Pack(args...)
		if err != nil {
			skipped = append(skipped, SkippedFunction{Name: method.Sig, Reason: fmt.Sprintf("encode arguments: %v", err)})
			continue
		}

		calldata := make([]byte, 0, len(method.ID)+len(packed))
		calldata = append(calldata, method.ID...)
		calldata = append(calldata, packed...)
		specs = append(specs, CallSpec{Name: method.Sig, Calldata: calldata})
	}
	return specs, skipped
}

// zeroArgs returns a zero value for each input, typed so abi.Pack accepts it.
func zeroArgs(inputs abi.Arguments) ([]any, error) {
	values := make([]any, len(inputs))
	for i, in := range inputs {
		v := reflect.New(in.Type.GetType()).Elem()
		fillZero(v)
		values[i] = v.Interface()
	}
	return values, nil
}

// fillZero makes a reflect value safe to ABI-encode. The go-ethereum ABI encoder
// represents integers as *big.Int, and a freshly reflected pointer is nil, which
// the encoder cannot handle. This walks the value and allocates any pointers it
// finds, recursing into arrays and tuples (structs) but stopping at a pointer so
// it never reaches a type's unexported internals.
func fillZero(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		// A freshly allocated *big.Int is already the value 0; no deeper fill.
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fillZero(v.Index(i))
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				fillZero(v.Field(i))
			}
		}
	}
	// Slices, strings, bools, and fixed-size numeric kinds are usable as-is at
	// their zero value.
}
