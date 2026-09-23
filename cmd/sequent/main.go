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

	confirmed := 0
	fmt.Fprintln(out, "Ordering dependencies (Writer -> Reader):")
	for _, e := range g.Edges {
		if imp := byEdge[edgeKey(e.Writer, e.Reader)]; imp.Confirmed {
			confirmed++
			fmt.Fprintf(out, "  %-11s %s -> %s   reader output %s -> %s\n",
				"[confirmed]", e.Writer, e.Reader, formatOutput(imp.Before), formatOutput(imp.After))
		} else {
			fmt.Fprintf(out, "  %-11s %s -> %s   slots: %s\n",
				"[shared]", e.Writer, e.Reader, formatSlots(e.Slots))
		}
	}

	fmt.Fprintf(out, "\n%d %s found: %d confirmed to change the reader's output, %d sharing state only.\n",
		len(g.Edges), dependencyWord(len(g.Edges)), confirmed, len(g.Edges)-confirmed)
	fmt.Fprintln(out, "Confirmed means running the writer first provably changes what the reader returns.")

	printSkipped(out, skipped)
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
