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
	printReport(out, path, len(fns), skipped, g)
	return nil
}

func printReport(out io.Writer, path string, analyzed int, skipped []analyze.SkippedFunction, g graph.Graph) {
	fmt.Fprintf(out, "Sequent: analyzed %s\n", path)
	fmt.Fprintf(out, "Functions analyzed: %d   Skipped: %d\n\n", analyzed, len(skipped))

	if len(g.Edges) == 0 {
		fmt.Fprintln(out, "No ordering dependencies found: no function writes storage that another reads.")
	} else {
		fmt.Fprintln(out, "Ordering dependencies (Writer -> Reader):")
		for _, e := range g.Edges {
			fmt.Fprintf(out, "  %s -> %s   slots: %s\n", e.Writer, e.Reader, formatSlots(e.Slots))
		}
		fmt.Fprintf(out, "\n%d %s found. These are the pairs where transaction order can change behavior.\n",
			len(g.Edges), dependencyWord(len(g.Edges)))
	}

	if len(skipped) > 0 {
		fmt.Fprintln(out, "\nSkipped functions (arguments could not be encoded):")
		for _, s := range skipped {
			fmt.Fprintf(out, "  %s: %s\n", s.Name, s.Reason)
		}
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
