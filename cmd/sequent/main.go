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

	"github.com/ethereum/go-ethereum/accounts/abi"
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
  sequent analyze [--json] [--tests <dir>] [--guard-tests <dir>] <foundry-artifact.json>
  sequent analyze [--json] [--tests <dir>] --abi <file> --bin <file>
      analyze a compiled contract, from a Foundry artifact or from separate ABI
      and bytecode files. --json prints machine-readable findings; --tests writes
      Foundry reproduction tests (which pass while the flaw exists); --guard-tests
      writes regression-guard tests (which pass once it is fixed)`

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
	testsDir := fs.String("tests", "", "write Foundry reproduction tests (effect present) to this directory")
	guardDir := fs.String("guard-tests", "", "write Foundry regression-guard tests (effect absent) to this directory")
	srcImport := fs.String("src", "", "source import path for guard tests, e.g. src/Vault.sol (defaults from the artifact path)")
	abiPath := fs.String("abi", "", "path to the ABI JSON (use with --bin instead of a Foundry artifact)")
	binPath := fs.String("bin", "", "path to the creation bytecode hex (use with --abi)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: sequent analyze [--json] [--tests <dir>] (<foundry-artifact.json> | --abi <file> --bin <file>)")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	c, path, err := loadContract(*abiPath, *binPath, fs.Args())
	if err != nil {
		if _, ok := err.(usageError); ok {
			fs.Usage()
			return 2
		}
		fmt.Fprintf(stderr, "sequent: %v\n", err)
		return 1
	}

	code, err := analyze.ResolveCreationCode(c.Bytecode, c.ABI, deployer)
	if err != nil {
		fmt.Fprintf(stderr, "sequent: %v\n", err)
		return 1
	}

	rep, err := analyzeContract(code, c.ABI, path)
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
		opts := gentest.Options{ContractName: contractName(path), Kind: gentest.Proof, CreationCode: code}
		if err := writeTests(*testsDir, opts, rep.Findings, stderr); err != nil {
			fmt.Fprintf(stderr, "sequent: write tests: %v\n", err)
			return 1
		}
	}
	if *guardDir != "" {
		name, imp, ok := guardSource(path, *srcImport)
		if !ok {
			fmt.Fprintln(stderr, "sequent: guard tests need the contract source; pass --src <path-to-source.sol> (or run on a Foundry artifact)")
			return 1
		}
		opts := gentest.Options{
			ContractName:    name,
			Kind:            gentest.Guard,
			SourceImport:    imp,
			ConstructorArgs: code[len(c.Bytecode):],
		}
		if err := writeTests(*guardDir, opts, rep.Findings, stderr); err != nil {
			fmt.Fprintf(stderr, "sequent: write guard tests: %v\n", err)
			return 1
		}
	}
	return 0
}

// guardSource resolves the Solidity contract name and its source import path for
// guard tests. An explicit --src override wins; otherwise it derives the path
// from a Foundry artifact layout (out/<File>.sol/<Name>.json -> src/<File>.sol).
// It returns ok=false when neither is available, for example with --abi/--bin.
func guardSource(artifactPath, override string) (name, srcImport string, ok bool) {
	name = contractName(artifactPath)
	if override != "" {
		return name, override, true
	}
	parent := filepath.Base(filepath.Dir(artifactPath))
	if strings.HasSuffix(parent, ".sol") {
		return name, "src/" + parent, true
	}
	return name, "", false
}

// usageError signals a misuse of the command, which maps to exit code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// loadContract picks the input source. Separate --abi/--bin files take precedence
// when either is given; otherwise a single Foundry artifact argument is required.
// The returned path is a label for the report and the reproduction-test name.
func loadContract(abiPath, binPath string, positional []string) (*contract.Contract, string, error) {
	if abiPath != "" || binPath != "" {
		if abiPath == "" || binPath == "" {
			return nil, "", usageError{"both --abi and --bin are required"}
		}
		if len(positional) != 0 {
			return nil, "", usageError{"do not pass an artifact together with --abi/--bin"}
		}
		c, err := contract.FromFiles(abiPath, binPath)
		return c, binPath, err
	}
	if len(positional) != 1 {
		return nil, "", usageError{"exactly one artifact path is required"}
	}
	c, err := contract.FromFoundryArtifact(positional[0])
	return c, positional[0], err
}

func analyzeContract(code []byte, a abi.ABI, path string) (report.Report, error) {
	fns, skipped, err := analyze.Fuzz(code, a, deployer, caller, analyze.FuzzConfig{})
	if err != nil {
		return report.Report{}, err
	}

	g := graph.Build(fns)

	impacts, err := analyze.Evaluate(code, a, deployer, caller, g.Edges, analyze.FuzzConfig{})
	if err != nil {
		return report.Report{}, err
	}

	return report.Build(path, len(fns), skipped, g, impacts), nil
}

// writeTests generates Foundry tests for the confirmed findings and writes them
// into dir. Progress notes go to notes (stderr) so they never mix into a JSON
// report on stdout.
func writeTests(dir string, opts gentest.Options, findings []report.Finding, notes io.Writer) error {
	src, n := gentest.Generate(opts, findings)

	label, suffix := "reproduction", "SequentRepro"
	if opts.Kind == gentest.Guard {
		label, suffix = "regression-guard", "SequentGuard"
	}

	if n == 0 {
		fmt.Fprintf(notes, "No confirmed findings; no %s test written.\n", label)
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, opts.ContractName+suffix+".t.sol")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(notes, "Wrote %d %s test(s) to %s\n", n, label, file)
	return nil
}

// contractName derives a contract name from a file path by dropping its
// extension, so Vault.json, Vault.abi, and Vault.bin all yield "Vault".
func contractName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
