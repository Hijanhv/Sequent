package trace

import (
	"reflect"
	"testing"
)

func TestParseSlot(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Slot
		wantErr bool
	}{
		{name: "zero", in: "0x0", want: SlotFromUint64(0)},
		{name: "no prefix", in: "1", want: SlotFromUint64(1)},
		{name: "with prefix", in: "0x05", want: SlotFromUint64(5)},
		{name: "odd length", in: "0xf", want: SlotFromUint64(15)},
		{name: "full width", in: "0x" + repeat("ff", 32), want: allBytes(0xff)},
		{name: "too wide", in: "0x" + repeat("ff", 33), wantErr: true},
		{name: "not hex", in: "0xzz", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSlot(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSlot(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSlot(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseSlot(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestSlotHexRoundTrip(t *testing.T) {
	for _, n := range []uint64{0, 1, 255, 256, 1 << 40} {
		s := SlotFromUint64(n)
		back, err := ParseSlot(s.Hex())
		if err != nil {
			t.Fatalf("ParseSlot(%s): %v", s.Hex(), err)
		}
		if back != s {
			t.Fatalf("round trip for %d: got %s, want %s", n, back, s)
		}
	}
}

func TestFunctionReadsWrites(t *testing.T) {
	f := Function{
		Name: "swap()",
		Accesses: []Access{
			{Kind: Read, Slot: SlotFromUint64(2)},
			{Kind: Write, Slot: SlotFromUint64(1)},
			{Kind: Read, Slot: SlotFromUint64(2)},  // duplicate read
			{Kind: Write, Slot: SlotFromUint64(1)}, // duplicate write
			{Kind: Read, Slot: SlotFromUint64(0)},
		},
	}

	wantReads := []Slot{SlotFromUint64(0), SlotFromUint64(2)}
	if got := f.Reads(); !reflect.DeepEqual(got, wantReads) {
		t.Fatalf("Reads() = %v, want %v", got, wantReads)
	}

	wantWrites := []Slot{SlotFromUint64(1)}
	if got := f.Writes(); !reflect.DeepEqual(got, wantWrites) {
		t.Fatalf("Writes() = %v, want %v", got, wantWrites)
	}
}

func TestKindString(t *testing.T) {
	if Read.String() != "read" || Write.String() != "write" {
		t.Fatalf("unexpected Kind strings: %s %s", Read, Write)
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func allBytes(b byte) Slot {
	var s Slot
	for i := range s {
		s[i] = b
	}
	return s
}
