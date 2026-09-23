// Package graph builds the interaction graph over a set of function traces.
//
// The graph answers one question: for which ordered pairs of functions does
// running one before the other change the second one's behavior? An edge from
// Writer to Reader means Writer writes at least one storage slot that Reader
// reads, so a party who controls transaction order can influence Reader by
// placing Writer ahead of it. These edges are the candidates a later stage
// evaluates for extractable value; everything with no shared state is pruned.
package graph

import (
	"sort"

	"github.com/Hijanhv/Sequent/internal/trace"
)

// Edge is an ordering dependency: Writer writes the listed slots and Reader
// reads them. Slots is sorted and non-empty.
type Edge struct {
	Writer string
	Reader string
	Slots  []trace.Slot
}

// Graph is the set of ordering dependencies among a group of functions. Edges
// is sorted by (Writer, Reader) so output is stable across runs.
type Graph struct {
	Edges []Edge
}

// Build computes the interaction graph for the given functions. Distinct
// functions are compared by position, so two entries with the same Name are
// still analyzed against each other. A function is never compared with itself.
func Build(fns []trace.Function) Graph {
	var edges []Edge
	for i := range fns {
		writes := sliceToSet(fns[i].Writes())
		if len(writes) == 0 {
			continue
		}
		for j := range fns {
			if i == j {
				continue
			}
			shared := intersect(writes, fns[j].Reads())
			if len(shared) == 0 {
				continue
			}
			edges = append(edges, Edge{
				Writer: fns[i].Name,
				Reader: fns[j].Name,
				Slots:  shared,
			})
		}
	}
	sortEdges(edges)
	return Graph{Edges: edges}
}

func sliceToSet(slots []trace.Slot) map[trace.Slot]struct{} {
	set := make(map[trace.Slot]struct{}, len(slots))
	for _, s := range slots {
		set[s] = struct{}{}
	}
	return set
}

// intersect returns the reads that are present in writes, sorted.
func intersect(writes map[trace.Slot]struct{}, reads []trace.Slot) []trace.Slot {
	var out []trace.Slot
	for _, r := range reads {
		if _, ok := writes[r]; ok {
			out = append(out, r)
		}
	}
	trace.SortSlots(out)
	return out
}

func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Writer != edges[j].Writer {
			return edges[i].Writer < edges[j].Writer
		}
		return edges[i].Reader < edges[j].Reader
	})
}
