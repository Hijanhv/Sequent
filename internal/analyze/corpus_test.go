package analyze

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestCorpusArgsPacksAllTypes(t *testing.T) {
	a := parseABI(t, sampleABI)
	addrs := addressCorpus(common.HexToAddress("0x000000000000000000000000000000000000cafe"))

	for _, m := range a.Methods {
		for r := 0; r < 4; r++ {
			args := corpusArgs(m.Inputs, r, addrs)
			if _, err := m.Inputs.Pack(args...); err != nil {
				t.Fatalf("pack %s round %d: %v", m.Sig, r, err)
			}
		}
	}
}

func TestAddressCorpusLedByCaller(t *testing.T) {
	caller := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	if got := addressCorpus(caller)[0]; got != caller {
		t.Fatalf("addressCorpus[0] = %s, want caller %s", got, caller)
	}
}

func TestCorpusVariesUintAcrossRounds(t *testing.T) {
	a := parseABI(t, `[{"type":"function","name":"f","stateMutability":"nonpayable","inputs":[{"name":"x","type":"uint256"}],"outputs":[]}]`)
	m := a.Methods["f"]
	addrs := addressCorpus(common.Address{})

	p0, err := m.Inputs.Pack(corpusArgs(m.Inputs, 0, addrs)...)
	if err != nil {
		t.Fatalf("pack round 0: %v", err)
	}
	p2, err := m.Inputs.Pack(corpusArgs(m.Inputs, 2, addrs)...)
	if err != nil {
		t.Fatalf("pack round 2: %v", err)
	}
	if bytes.Equal(p0, p2) {
		t.Fatal("expected different encodings across rounds for a uint256 argument")
	}
}
