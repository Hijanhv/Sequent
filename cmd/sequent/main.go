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
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Hijanhv/Sequent/internal/analyze"
	"github.com/Hijanhv/Sequent/internal/contract"
	"github.com/Hijanhv/Sequent/internal/gentest"
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
  sequent analyze [--json] [--tests <dir>] <foundry-artifact.json>
      analyze a compiled contract; --json prints machine-readable findings,
      --tests writes a Foundry reproduction test for the confirmed findings`

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
	testsDir := fs.String("tests", "", "write a Foundry reproduction test to this directory")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: sequent analyze [--json] [--tests <dir>] <foundry-artifact.json>")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	path := fs.Arg(0)

	c, err := contract.FromFoundryArtifact(path)
	if err != nil {
		fmt.Fprintf(stderr, "sequent: %v\n", err)
		return 1
	}

	rep, err := analyzeContract(c, path)
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

	if *testsDir != "" {
		if err := writeReproTests(*testsDir, path, c.Bytecode, rep, stderr); err != nil {
			fmt.Fprintf(stderr, "sequent: write tests: %v\n", err)
			return 1
		}
	}
	return 0
}

func analyzeContract(c *contract.Contract, path string) (report.Report, error) {
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

// writeReproTests generates a Foundry test reproducing the confirmed findings and
// writes it next to the given directory. Progress notes go to notes (stderr) so
// they never mix into a JSON report on stdout.
func writeReproTests(dir, artifactPath string, bytecode []byte, rep report.Report, notes io.Writer) error {
	name := strings.TrimSuffix(filepath.Base(artifactPath), ".json")
	src, n := gentest.Generate(name, bytecode, rep.Findings)
	if n == 0 {
		fmt.Fprintln(notes, "No confirmed findings; no reproduction test written.")
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, name+"SequentRepro.t.sol")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(notes, "Wrote %d reproduction test(s) to %s\n", n, file)
	return nil
}
