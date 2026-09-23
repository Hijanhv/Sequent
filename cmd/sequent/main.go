// Command sequent analyzes a compiled contract and reports the ordering
// dependencies between its functions: the pairs where one function writes
// storage that another reads, and so where transaction order can change
// behavior. It ties together the loader, the embedded EVM, the interaction
// graph, the scenario evaluation, and the report.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/analyze"
	"github.com/Hijanhv/Sequent/internal/contract"
	"github.com/Hijanhv/Sequent/internal/graph"
	"github.com/Hijanhv/Sequent/internal/report"
)

// Fixed accounts for analysis. Their values do not matter to storage-footprint
// analysis; they only need to be stable so runs are reproducible.
var (
	deployer = common.HexToAddress("0x00000000000000000000000000000000000d0000")
	caller   = common.HexToAddress("0x000000000000000000000000000000000000cafe")
)

const usage = `sequent - an MEV exposure scanner for smart contracts

usage:
  sequent analyze [--json] <foundry-artifact.json>   analyze a compiled contract`

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
		return runAnalyze(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

func runAnalyze(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "output findings as JSON")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: sequent analyze [--json] <foundry-artifact.json>")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	rep, err := analyzeArtifact(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "sequent: %v\n", err)
		return 1
	}

	if *asJSON {
		if err := rep.WriteJSON(stdout); err != nil {
			fmt.Fprintf(stderr, "sequent: write json: %v\n", err)
			return 1
		}
	} else {
		rep.WriteText(stdout)
	}
	return 0
}

func analyzeArtifact(path string) (report.Report, error) {
	c, err := contract.FromFoundryArtifact(path)
	if err != nil {
		return report.Report{}, err
	}

	fns, skipped, err := analyze.Fuzz(c.Bytecode, c.ABI, deployer, caller, analyze.FuzzConfig{})
	if err != nil {
		return report.Report{}, fmt.Errorf("%w (constructors that require arguments are not yet supported)", err)
	}

	g := graph.Build(fns)

	impacts, err := analyze.Evaluate(c.Bytecode, c.ABI, deployer, caller, g.Edges, analyze.FuzzConfig{})
	if err != nil {
		return report.Report{}, err
	}

	return report.Build(path, len(fns), skipped, g, impacts), nil
}
