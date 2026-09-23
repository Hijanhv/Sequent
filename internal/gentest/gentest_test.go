package gentest

import (
	"math/big"
	"strings"
	"testing"

	"github.com/Hijanhv/Sequent/internal/report"
)

func TestGenerateNoConfirmedFindings(t *testing.T) {
	src, n := Generate("Vault", []byte{0x01, 0x02}, []report.Finding{
		{Writer: "a()", Reader: "b()", Confirmed: false},
	})
	if n != 0 || src != "" {
		t.Fatalf("expected empty output for no confirmed findings, got n=%d, src=%q", n, src)
	}
}

func TestGenerateProducesTestPerConfirmedFinding(t *testing.T) {
	findings := []report.Finding{
		{
			Writer: "deposit()", Reader: "balanceOf(address)", Confirmed: true,
			Effect: "value-change", Delta: big.NewInt(1),
			WriterCall: []byte{0xd0, 0xe3, 0x0d, 0xb0},
			ReaderCall: []byte{0x70, 0xa0, 0x82, 0x31},
		},
		{
			Writer: "deposit()", Reader: "withdraw()", Confirmed: true,
			Effect:     "revert-flip",
			WriterCall: []byte{0xd0, 0xe3, 0x0d, 0xb0},
			ReaderCall: []byte{0x3c, 0xcf, 0xd6, 0x0b},
		},
		{Writer: "x()", Reader: "y()", Confirmed: false},
	}

	src, n := Generate("Vault", []byte{0xaa, 0xbb}, findings)
	if n != 2 {
		t.Fatalf("expected 2 tests, got %d", n)
	}

	wants := []string{
		"contract VaultSequentRepro",
		"Vm(0x7109709ECfa91a80626fF3989D68f67F5b1DD12D)",
		`hex"aabb"`,
		"function test_deposit_before_balanceOf_address() public",
		"function test_deposit_before_withdraw() public",
		`hex"d0e30db0"`,
		`hex"70a08231"`,
		`hex"3ccfd60b"`,
		"reader value changes by 1",
		"reader flips between reverting and succeeding",
	}
	for _, w := range wants {
		if !strings.Contains(src, w) {
			t.Fatalf("generated source missing %q; got:\n%s", w, src)
		}
	}
}

func TestGenerateSkipsConfirmedWithoutCalldata(t *testing.T) {
	// A confirmed finding with no recorded calldata cannot be reproduced.
	src, n := Generate("T", []byte{0x01}, []report.Finding{
		{Writer: "a()", Reader: "b()", Confirmed: true},
	})
	if n != 0 || src != "" {
		t.Fatalf("expected finding without calldata to be skipped, got n=%d", n)
	}
}

func TestGenerateUniqueFunctionNames(t *testing.T) {
	findings := []report.Finding{
		{Writer: "f()", Reader: "g()", Confirmed: true, WriterCall: []byte{0x01}, ReaderCall: []byte{0x02}},
		{Writer: "f()", Reader: "g()", Confirmed: true, WriterCall: []byte{0x01}, ReaderCall: []byte{0x02}},
	}
	src, n := Generate("T", []byte{0x01}, findings)
	if n != 2 {
		t.Fatalf("expected 2 tests, got %d", n)
	}
	if !strings.Contains(src, "function test_f_before_g() public") {
		t.Fatalf("missing first test name:\n%s", src)
	}
	if !strings.Contains(src, "function test_f_before_g_2() public") {
		t.Fatalf("missing de-duplicated second test name:\n%s", src)
	}
}

func TestIdentifierSanitizes(t *testing.T) {
	cases := map[string]string{
		"balanceOf(address)": "balanceOf_address",
		"deposit()":          "deposit",
		"a(uint256,bool)":    "a_uint256_bool",
		"":                   "fn",
		"()":                 "fn",
	}
	for in, want := range cases {
		if got := identifier(in); got != want {
			t.Fatalf("identifier(%q) = %q, want %q", in, got, want)
		}
	}
}
