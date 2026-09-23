package graph

import (
	"reflect"
	"testing"

	"github.com/Hijanhv/Sequent/internal/trace"
)

func read(n uint64) trace.Access {
	return trace.Access{Kind: trace.Read, Slot: trace.SlotFromUint64(n)}
}
func write(n uint64) trace.Access {
	return trace.Access{Kind: trace.Write, Slot: trace.SlotFromUint64(n)}
}

func TestBuildSingleEdge(t *testing.T) {
	fns := []trace.Function{
		{Name: "attack()", Accesses: []trace.Access{write(1)}},
		{Name: "victim()", Accesses: []trace.Access{read(1)}},
	}
	g := Build(fns)

	want := []Edge{
		{Writer: "attack()", Reader: "victim()", Slots: []trace.Slot{trace.SlotFromUint64(1)}},
	}
	if !reflect.DeepEqual(g.Edges, want) {
		t.Fatalf("edges = %v, want %v", g.Edges, want)
	}
}

func TestBuildNoOverlap(t *testing.T) {
	fns := []trace.Function{
		{Name: "a()", Accesses: []trace.Access{write(1)}},
		{Name: "b()", Accesses: []trace.Access{read(2)}},
	}
	if g := Build(fns); len(g.Edges) != 0 {
		t.Fatalf("expected no edges, got %v", g.Edges)
	}
}

func TestBuildReadOnlyFunctionsHaveNoEdges(t *testing.T) {
	fns := []trace.Function{
		{Name: "view1()", Accesses: []trace.Access{read(1)}},
		{Name: "view2()", Accesses: []trace.Access{read(1)}},
	}
	if g := Build(fns); len(g.Edges) != 0 {
		t.Fatalf("read-only functions must not produce edges, got %v", g.Edges)
	}
}

func TestBuildBidirectional(t *testing.T) {
	// a writes slot 1 and reads slot 2; b writes slot 2 and reads slot 1.
	// Each can influence the other, so both directions appear.
	fns := []trace.Function{
		{Name: "a()", Accesses: []trace.Access{write(1), read(2)}},
		{Name: "b()", Accesses: []trace.Access{write(2), read(1)}},
	}
	g := Build(fns)

	want := []Edge{
		{Writer: "a()", Reader: "b()", Slots: []trace.Slot{trace.SlotFromUint64(1)}},
		{Writer: "b()", Reader: "a()", Slots: []trace.Slot{trace.SlotFromUint64(2)}},
	}
	if !reflect.DeepEqual(g.Edges, want) {
		t.Fatalf("edges = %v, want %v", g.Edges, want)
	}
}

func TestBuildMultipleSharedSlotsSorted(t *testing.T) {
	fns := []trace.Function{
		{Name: "w()", Accesses: []trace.Access{write(3), write(1), write(2)}},
		{Name: "r()", Accesses: []trace.Access{read(2), read(1), read(3)}},
	}
	g := Build(fns)

	if len(g.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(g.Edges))
	}
	want := []trace.Slot{trace.SlotFromUint64(1), trace.SlotFromUint64(2), trace.SlotFromUint64(3)}
	if !reflect.DeepEqual(g.Edges[0].Slots, want) {
		t.Fatalf("slots = %v, want %v", g.Edges[0].Slots, want)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	fns := []trace.Function{
		{Name: "c()", Accesses: []trace.Access{write(1), read(2)}},
		{Name: "a()", Accesses: []trace.Access{write(2), read(1)}},
		{Name: "b()", Accesses: []trace.Access{write(1), write(2)}},
	}
	first := Build(fns)
	second := Build(fns)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Build is not deterministic:\n%v\n%v", first, second)
	}

	// Edges must be sorted by (Writer, Reader).
	for i := 1; i < len(first.Edges); i++ {
		prev, cur := first.Edges[i-1], first.Edges[i]
		if prev.Writer > cur.Writer || (prev.Writer == cur.Writer && prev.Reader > cur.Reader) {
			t.Fatalf("edges not sorted at %d: %v then %v", i, prev, cur)
		}
	}
}

func TestBuildSkipsSelfComparison(t *testing.T) {
	// A function that reads and writes the same slot must not edge to itself.
	fns := []trace.Function{
		{Name: "solo()", Accesses: []trace.Access{write(1), read(1)}},
	}
	if g := Build(fns); len(g.Edges) != 0 {
		t.Fatalf("expected no self edge, got %v", g.Edges)
	}
}
