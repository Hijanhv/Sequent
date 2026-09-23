// Command sequent analyzes a compiled contract and prints the ordering
// dependencies between its functions: the pairs where one function writes
// storage that another reads, and so where transaction order can change
// behavior. It is the entry point that ties the loader, the embedded EVM, and
// the interaction graph together.
package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"os"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/analyze"
	"github.com/Hijanhv/Sequent/internal/contract"
	"github.com/Hijanhv/Sequent/internal/graph"
	"github.com/Hijanhv/Sequent/internal/trace"
)

// Fixed accounts for analysis. Their values do not matter to storage-footprint
// analysis; they only need to be stable so runs are reproducible.
var (
	deployer = common.HexToAddress("0x00000000000000000000000000000000000d0000")
	caller   = common.HexToAddress("0x000000000000000000000000000000000000cafe")
)

const usage = `sequent - an MEV exposure scanner for smart contracts

usage:
  sequent analyze <foundry-artifact.json>   analyze a compiled contract`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the program body, split out from main so it can be tested. It returns
// the process exit code: 0 on success, 1 on an analysis error, 2 on misuse.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}

	switch args[0] {
	case "analyze":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: sequent analyze <foundry-artifact.json>")
			return 2
		}
		if err := analyzeArtifact(args[1], stdout); err != nil {
			fmt.Fprintf(stderr, "sequent: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

func analyzeArtifact(path string, out io.Writer) error {
	c, err := contract.FromFoundryArtifact(path)
	if err != nil {
		return err
	}

	fns, skipped, err := analyze.Fuzz(c.Bytecode, c.ABI, deployer, caller, analyze.FuzzConfig{})
	if err != nil {
		return fmt.Errorf("%w (constructors that require arguments are not yet supported)", err)
	}

	g := graph.Build(fns)

	impacts, err := analyze.Evaluate(c.Bytecode, c.ABI, deployer, caller, g.Edges, analyze.FuzzConfig{})
	if err != nil {
		return err
	}

	printReport(out, path, len(fns), skipped, g, impacts)
	return nil
}

func printReport(out io.Writer, path string, analyzed int, skipped []analyze.SkippedFunction, g graph.Graph, impacts []analyze.Impact) {
	fmt.Fprintf(out, "Sequent: analyzed %s\n", path)
	fmt.Fprintf(out, "Functions analyzed: %d   Skipped: %d\n\n", analyzed, len(skipped))

	if len(g.Edges) == 0 {
		fmt.Fprintln(out, "No ordering dependencies found: no function writes storage that another reads.")
		printSkipped(out, skipped)
		return
	}

	byEdge := make(map[string]analyze.Impact, len(impacts))
	for _, imp := range impacts {
		byEdge[edgeKey(imp.Writer, imp.Reader)] = imp
	}

	findings := make([]finding, 0, len(g.Edges))
	for _, e := range g.Edges {
		imp := byEdge[edgeKey(e.Writer, e.Reader)]
		findings = append(findings, finding{edge: e, impact: imp, sev: classify(imp)})
	}
	rankFindings(findings)

	var high, medium, low int
	fmt.Fprintln(out, "Findings (most severe first):")
	for _, f := range findings {
		switch f.sev {
		case sevHigh:
			high++
		case sevMedium:
			medium++
		default:
			low++
		}
		fmt.Fprintf(out, "  %-6s %s -> %s   %s\n", f.sev, f.edge.Writer, f.edge.Reader, describe(f))
	}

	fmt.Fprintf(out, "\nSummary: %d high, %d medium, %d low (of %d %s).\n",
		high, medium, low, len(findings), dependencyWord(len(findings)))
	fmt.Fprintln(out, "High: ordering provably changes a value the reader returns, or flips it between")
	fmt.Fprintln(out, "reverting and succeeding. Low: shares state but no effect was observed.")

	printSkipped(out, skipped)
}

// finding pairs a dependency edge with the measured impact of reordering it.
type finding struct {
	edge   graph.Edge
	impact analyze.Impact
	sev    severity
}

type severity int

const (
	sevLow severity = iota
	sevMedium
	sevHigh
)

func (s severity) String() string {
	switch s {
	case sevHigh:
		return "HIGH"
	case sevMedium:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// classify scores a dependency. Moving a value the reader returns, or flipping it
// between reverting and succeeding, is the clearest exploitable effect and ranks
// highest. A confirmed but less legible change is medium. A shared slot with no
// observed effect is low: a lead, not a finding.
func classify(imp analyze.Impact) severity {
	if !imp.Confirmed {
		return sevLow
	}
	if imp.BeforeReverted != imp.AfterReverted {
		return sevHigh
	}
	if imp.Delta != nil && imp.Delta.Sign() != 0 {
		return sevHigh
	}
	return sevMedium
}

// rankFindings orders findings by severity, then by the magnitude of the value
// change, then by name, so the output is deterministic and the worst is first.
func rankFindings(findings []finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].sev != findings[j].sev {
			return findings[i].sev > findings[j].sev
		}
		mi, mj := magnitude(findings[i].impact), magnitude(findings[j].impact)
		if c := mi.Cmp(mj); c != 0 {
			return c > 0
		}
		if findings[i].edge.Writer != findings[j].edge.Writer {
			return findings[i].edge.Writer < findings[j].edge.Writer
		}
		return findings[i].edge.Reader < findings[j].edge.Reader
	})
}

// magnitude is the absolute value of a finding's numeric delta, or zero when
// there is none.
func magnitude(imp analyze.Impact) *big.Int {
	if imp.Delta == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Abs(imp.Delta)
}

// describe renders the human-readable effect of a finding.
func describe(f finding) string {
	imp := f.impact
	if !imp.Confirmed {
		return fmt.Sprintf("shares slots %s (no effect observed)", formatSlots(f.edge.Slots))
	}
	if imp.BeforeReverted != imp.AfterReverted {
		return revertState(imp.BeforeReverted) + " -> " + revertState(imp.AfterReverted)
	}
	if imp.Delta != nil {
		return fmt.Sprintf("reader value %s -> %s (change %s)",
			formatOutput(imp.Before), formatOutput(imp.After), signedDecimal(imp.Delta))
	}
	return fmt.Sprintf("reader output %s -> %s", formatOutput(imp.Before), formatOutput(imp.After))
}

func revertState(reverted bool) string {
	if reverted {
		return "reverted"
	}
	return "succeeded"
}

func signedDecimal(n *big.Int) string {
	if n.Sign() >= 0 {
		return "+" + n.String()
	}
	return n.String()
}

func printSkipped(out io.Writer, skipped []analyze.SkippedFunction) {
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintln(out, "\nSkipped functions (arguments could not be encoded):")
	for _, s := range skipped {
		fmt.Fprintf(out, "  %s: %s\n", s.Name, s.Reason)
	}
}

func edgeKey(writer, reader string) string {
	return writer + "\x00" + reader
}

// formatOutput renders a reader's return value. A 32-byte word, the common case,
// is shown as a decimal number; anything else is shown as truncated hex.
func formatOutput(b []byte) string {
	switch {
	case len(b) == 0:
		return "(no output)"
	case len(b) == 32:
		return new(big.Int).SetBytes(b).String()
	default:
		s := hex.EncodeToString(b)
		if len(s) > 32 {
			s = s[:32] + "..."
		}
		return "0x" + s
	}
}

// formatSlots renders slots compactly, trimming leading zero bytes so a simple
// slot like number 2 prints as 0x02 rather than 64 hex characters.
func formatSlots(slots []trace.Slot) string {
	parts := make([]string, len(slots))
	for i, s := range slots {
		parts[i] = formatSlot(s)
	}
	return strings.Join(parts, ", ")
}

func formatSlot(s trace.Slot) string {
	b := s[:]
	i := 0
	for i < len(b)-1 && b[i] == 0 {
		i++
	}
	return "0x" + hex.EncodeToString(b[i:])
}

func dependencyWord(n int) string {
	if n == 1 {
		return "dependency"
	}
	return "dependencies"
}
