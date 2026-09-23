// Package trace defines the storage-access model that the rest of Sequent
// analyzes. A trace records, in execution order, every storage slot a
// contract function reads or writes. These traces are the raw material for
// the interaction graph: when one function writes a slot another function
// reads, the outcome of the second call depends on transaction ordering.
package trace

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Slot is a 32-byte EVM storage slot key.
type Slot [32]byte

// SlotFromUint64 returns the slot whose key is n, big-endian in the low bytes.
// It is a convenience for simple slot layouts and for tests.
func SlotFromUint64(n uint64) Slot {
	var s Slot
	for i := 0; i < 8; i++ {
		s[31-i] = byte(n >> (8 * i))
	}
	return s
}

// ParseSlot parses a hex slot key, with or without a "0x" prefix. The value is
// left-padded to 32 bytes. It returns an error for non-hex input or a key
// wider than 32 bytes.
func ParseSlot(s string) (Slot, error) {
	raw := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(raw)%2 == 1 {
		raw = "0" + raw
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return Slot{}, fmt.Errorf("parse slot %q: %w", s, err)
	}
	if len(b) > 32 {
		return Slot{}, fmt.Errorf("parse slot %q: %d bytes exceeds 32", s, len(b))
	}
	var out Slot
	copy(out[32-len(b):], b)
	return out, nil
}

// Hex returns the slot key as a 0x-prefixed, 64-character hex string.
func (s Slot) Hex() string {
	return "0x" + hex.EncodeToString(s[:])
}

// String implements fmt.Stringer.
func (s Slot) String() string { return s.Hex() }

// Kind distinguishes a storage read from a storage write.
type Kind uint8

const (
	Read Kind = iota
	Write
)

// String implements fmt.Stringer.
func (k Kind) String() string {
	switch k {
	case Read:
		return "read"
	case Write:
		return "write"
	default:
		return "unknown"
	}
}

// Access is a single storage read or write observed during execution.
type Access struct {
	Kind Kind
	Slot Slot
}

// Function is the ordered sequence of storage accesses made by one contract
// function during a single call. Name is a human-readable label such as the
// function signature.
type Function struct {
	Name     string
	Accesses []Access
}

// Reads returns the unique slots the function read, sorted for determinism.
func (f Function) Reads() []Slot { return f.slots(Read) }

// Writes returns the unique slots the function wrote, sorted for determinism.
func (f Function) Writes() []Slot { return f.slots(Write) }

func (f Function) slots(kind Kind) []Slot {
	seen := make(map[Slot]struct{})
	var out []Slot
	for _, a := range f.Accesses {
		if a.Kind != kind {
			continue
		}
		if _, ok := seen[a.Slot]; ok {
			continue
		}
		seen[a.Slot] = struct{}{}
		out = append(out, a.Slot)
	}
	SortSlots(out)
	return out
}

// SortSlots orders slots by their raw key bytes.
func SortSlots(s []Slot) {
	sort.Slice(s, func(i, j int) bool {
		for k := 0; k < 32; k++ {
			if s[i][k] != s[j][k] {
				return s[i][k] < s[j][k]
			}
		}
		return false
	})
}
