package analyze

import (
	"math/big"
	"reflect"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// The corpus is the small set of candidate values Sequent tries for each
// argument type. It is deliberately tiny and fixed rather than random, so a run
// is reproducible and so the values line up across functions: if one function
// writes a mapping keyed by an address and another reads it, they only connect
// when both are called with the same address. Reusing the caller address as a
// candidate is what makes a function that uses msg.sender line up with one that
// takes an address argument.
var (
	bigCorpus       = []int64{1, 0, 2, 1_000_000}
	smallUintCorpus = []uint64{1, 0, 2, 7}
	smallIntCorpus  = []int64{1, 0, 2, -1}
	stringCorpus    = []string{"sequent", ""}
	bytesCorpus     = [][]byte{{0xde, 0xad, 0xbe, 0xef}, {}}
)

var bigIntType = reflect.TypeOf((*big.Int)(nil))

// addressCorpus returns candidate addresses for a run, led by the caller so that
// msg.sender-keyed storage and address-argument-keyed storage can meet.
func addressCorpus(caller common.Address) []common.Address {
	return []common.Address{
		caller,
		{}, // the zero address
		common.HexToAddress("0x000000000000000000000000000000000000beef"),
		common.HexToAddress("0x00000000000000000000000000000000cafef00d"),
	}
}

// corpusArgs builds one value per input for the given round, choosing from the
// corpus. Values are typed to match what the ABI encoder expects.
func corpusArgs(inputs abi.Arguments, round int, addrs []common.Address) []any {
	values := make([]any, len(inputs))
	for i, in := range inputs {
		v := reflect.New(in.Type.GetType()).Elem()
		setFromCorpus(v, round, addrs)
		values[i] = v.Interface()
	}
	return values
}

// setFromCorpus fills v with a round-dependent value from the corpus. Unknown
// kinds are left at their zero value, so encoding never fails on an unexpected
// type; it just does not vary that argument.
func setFromCorpus(v reflect.Value, round int, addrs []common.Address) {
	switch v.Kind() {
	case reflect.Ptr:
		if v.Type() == bigIntType {
			v.Set(reflect.ValueOf(big.NewInt(bigCorpus[round%len(bigCorpus)])))
		} else {
			v.Set(reflect.New(v.Type().Elem()))
		}
	case reflect.Bool:
		v.SetBool(round%2 == 0)
	case reflect.String:
		v.SetString(stringCorpus[round%len(stringCorpus)])
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(smallUintCorpus[round%len(smallUintCorpus)])
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(smallIntCorpus[round%len(smallIntCorpus)])
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes(bytesCorpus[round%len(bytesCorpus)])
		} else {
			elem := reflect.New(v.Type().Elem()).Elem()
			setFromCorpus(elem, round, addrs)
			v.Set(reflect.Append(v, elem))
		}
	case reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			fillByteArray(v, round, addrs)
		} else {
			for i := 0; i < v.Len(); i++ {
				setFromCorpus(v.Index(i), round+i, addrs)
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				setFromCorpus(v.Field(i), round, addrs)
			}
		}
	}
}

// fillByteArray fills a fixed-size byte array. A 20-byte array is an address, so
// it draws from the address corpus to keep keys aligned across functions. Other
// widths (bytesN, bytes32) get a simple round-dependent pattern.
func fillByteArray(v reflect.Value, round int, addrs []common.Address) {
	n := v.Len()
	var src []byte
	if n == common.AddressLength {
		addr := addrs[round%len(addrs)]
		src = addr.Bytes()
	} else {
		src = make([]byte, n)
		for i := range src {
			src[i] = byte(round + 1)
		}
	}
	reflect.Copy(v, reflect.ValueOf(src))
}
