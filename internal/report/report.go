// Package report turns the raw analysis results, the interaction graph and the
// per-edge impacts, into a ranked set of findings, and renders them either as
// text for a person or as JSON for another tool. Keeping this separate from the
// command means the ranking and formatting can be tested on their own.
package report

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"

	"github.com/Hijanhv/Sequent/internal/analyze"
	"github.com/Hijanhv/Sequent/internal/graph"
	"github.com/Hijanhv/Sequent/internal/trace"
)

// Severity ranks how serious a finding is.
type Severity int

const (
	Low Severity = iota
	Medium
	High
)

func (s Severity) String() string {
	switch s {
	case High:
		return "HIGH"
	case Medium:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// Finding is one dependency edge with the measured effect of reordering it.
type Finding struct {
	Severity       Severity
	Writer         string
	Reader         string
	Confirmed      bool
	Effect         string // value-change, revert-flip, output-change, or shared
	Before         []byte
	After          []byte
	BeforeReverted bool
	AfterReverted  bool
	Delta          *big.Int
	Slots          []trace.Slot

	// WriterCall and ReaderCall are the calldata that produced a confirmed
	// effect, used to generate a reproduction test.
	WriterCall []byte
	ReaderCall []byte

	// MaxRepeats and MaxDelta describe a stacked, multi-transaction front-run:
	// when MaxRepeats is greater than 1, running the writer that many times before
	// the reader produces the larger change MaxDelta, so the effect compounds
	// across transactions.
	MaxRepeats int
	MaxDelta   *big.Int
}

// Skipped names a function that could not be analyzed.
type Skipped struct {
	Name   string
	Reason string
}

// Report is the full result of analyzing one contract.
type Report struct {
	Contract          string
	FunctionsAnalyzed int
	Findings          []Finding
	Skipped           []Skipped
	High              int
	Medium            int
	Low               int
}

// Build assembles a ranked report from the graph and the per-edge impacts.
func Build(contract string, analyzed int, skipped []analyze.SkippedFunction, g graph.Graph, impacts []analyze.Impact) Report {
	byEdge := make(map[string]analyze.Impact, len(impacts))
	for _, imp := range impacts {
		byEdge[key(imp.Writer, imp.Reader)] = imp
	}

	findings := make([]Finding, 0, len(g.Edges))
	for _, e := range g.Edges {
		imp := byEdge[key(e.Writer, e.Reader)]
		findings = append(findings, Finding{
			Severity:       classify(imp),
			Writer:         e.Writer,
			Reader:         e.Reader,
			Confirmed:      imp.Confirmed,
			Effect:         effectOf(imp),
			Before:         imp.Before,
			After:          imp.After,
			BeforeReverted: imp.BeforeReverted,
			AfterReverted:  imp.AfterReverted,
			Delta:          imp.Delta,
			Slots:          e.Slots,
			WriterCall:     imp.WriterCall,
			ReaderCall:     imp.ReaderCall,
			MaxRepeats:     imp.MaxRepeats,
			MaxDelta:       imp.MaxDelta,
		})
	}
	rank(findings)

	r := Report{Contract: contract, FunctionsAnalyzed: analyzed, Findings: findings}
	for _, s := range skipped {
		r.Skipped = append(r.Skipped, Skipped{Name: s.Name, Reason: s.Reason})
	}
	for _, f := range findings {
		switch f.Severity {
		case High:
			r.High++
		case Medium:
			r.Medium++
		default:
			r.Low++
		}
	}
	return r
}

// classify scores a dependency. Moving a value the reader returns, or flipping it
// between reverting and succeeding, is the clearest exploitable effect and ranks
// highest. A confirmed but less legible change is medium. A shared slot with no
// observed effect is low: a lead, not a finding.
func classify(imp analyze.Impact) Severity {
	if !imp.Confirmed {
		return Low
	}
	if imp.BeforeReverted != imp.AfterReverted {
		return High
	}
	if imp.Delta != nil && imp.Delta.Sign() != 0 {
		return High
	}
	return Medium
}

func effectOf(imp analyze.Impact) string {
	switch {
	case !imp.Confirmed:
		return "shared"
	case imp.BeforeReverted != imp.AfterReverted:
		return "revert-flip"
	case imp.Delta != nil:
		return "value-change"
	default:
		return "output-change"
	}
}

// rank orders findings by severity, then by the magnitude of the value change,
// then by name, so the output is deterministic and the worst comes first.
func rank(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity > findings[j].Severity
		}
		if c := magnitude(findings[i]).Cmp(magnitude(findings[j])); c != 0 {
			return c > 0
		}
		if findings[i].Writer != findings[j].Writer {
			return findings[i].Writer < findings[j].Writer
		}
		return findings[i].Reader < findings[j].Reader
	})
}

func magnitude(f Finding) *big.Int {
	if f.Delta == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Abs(f.Delta)
}

func key(writer, reader string) string {
	return writer + "\x00" + reader
}

// WriteText renders the report for a person.
func (r Report) WriteText(w io.Writer) {
	fmt.Fprintf(w, "Sequent: analyzed %s\n", r.Contract)
	fmt.Fprintf(w, "Functions analyzed: %d   Skipped: %d\n\n", r.FunctionsAnalyzed, len(r.Skipped))

	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "No ordering dependencies found: no function writes storage that another reads.")
		r.writeSkipped(w)
		return
	}

	fmt.Fprintln(w, "Findings (most severe first):")
	for _, f := range r.Findings {
		fmt.Fprintf(w, "  %-6s %s -> %s   %s\n", f.Severity, f.Writer, f.Reader, describe(f))
	}

	fmt.Fprintf(w, "\nSummary: %d high, %d medium, %d low (of %d %s).\n",
		r.High, r.Medium, r.Low, len(r.Findings), dependencyWord(len(r.Findings)))
	fmt.Fprintln(w, "High: ordering provably changes a value the reader returns, or flips it between")
	fmt.Fprintln(w, "reverting and succeeding. Low: shares state but no effect was observed.")

	r.writeSkipped(w)
}

func (r Report) writeSkipped(w io.Writer) {
	if len(r.Skipped) == 0 {
		return
	}
	fmt.Fprintln(w, "\nSkipped functions (arguments could not be encoded):")
	for _, s := range r.Skipped {
		fmt.Fprintf(w, "  %s: %s\n", s.Name, s.Reason)
	}
}

func describe(f Finding) string {
	if !f.Confirmed {
		return fmt.Sprintf("shares slots %s (no effect observed)", formatSlots(f.Slots))
	}
	if f.BeforeReverted != f.AfterReverted {
		return revertState(f.BeforeReverted) + " -> " + revertState(f.AfterReverted)
	}
	if f.Delta != nil {
		base := fmt.Sprintf("reader value %s -> %s (change %s)",
			formatOutput(f.Before), formatOutput(f.After), signedDecimal(f.Delta))
		if f.MaxRepeats > 1 && f.MaxDelta != nil {
			base += fmt.Sprintf("; compounds to %s over %d transactions", signedDecimal(f.MaxDelta), f.MaxRepeats)
		}
		return base
	}
	return fmt.Sprintf("reader output %s -> %s", formatOutput(f.Before), formatOutput(f.After))
}

// WriteJSON renders the report as indented JSON for another tool to consume.
func (r Report) WriteJSON(w io.Writer) error {
	out := jsonReport{
		Contract:          r.Contract,
		FunctionsAnalyzed: r.FunctionsAnalyzed,
		Summary:           jsonSummary{High: r.High, Medium: r.Medium, Low: r.Low},
	}
	for _, f := range r.Findings {
		jf := jsonFinding{
			Severity:       f.Severity.String(),
			Writer:         f.Writer,
			Reader:         f.Reader,
			Confirmed:      f.Confirmed,
			Effect:         f.Effect,
			BeforeReverted: f.BeforeReverted,
			AfterReverted:  f.AfterReverted,
			Slots:          formatSlotList(f.Slots),
		}
		if f.Confirmed {
			jf.ReaderBefore = hexBytes(f.Before)
			jf.ReaderAfter = hexBytes(f.After)
		}
		if f.Delta != nil {
			jf.Delta = f.Delta.String()
		}
		if f.MaxRepeats > 1 && f.MaxDelta != nil {
			jf.MaxRepeats = f.MaxRepeats
			jf.MaxDelta = f.MaxDelta.String()
		}
		out.Findings = append(out.Findings, jf)
	}
	for _, s := range r.Skipped {
		out.Skipped = append(out.Skipped, jsonSkipped{Name: s.Name, Reason: s.Reason})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

type jsonReport struct {
	Contract          string        `json:"contract"`
	FunctionsAnalyzed int           `json:"functionsAnalyzed"`
	Findings          []jsonFinding `json:"findings"`
	Skipped           []jsonSkipped `json:"skipped,omitempty"`
	Summary           jsonSummary   `json:"summary"`
}

type jsonFinding struct {
	Severity       string   `json:"severity"`
	Writer         string   `json:"writer"`
	Reader         string   `json:"reader"`
	Confirmed      bool     `json:"confirmed"`
	Effect         string   `json:"effect"`
	ReaderBefore   string   `json:"readerBefore,omitempty"`
	ReaderAfter    string   `json:"readerAfter,omitempty"`
	BeforeReverted bool     `json:"beforeReverted"`
	AfterReverted  bool     `json:"afterReverted"`
	Delta          string   `json:"delta,omitempty"`
	MaxRepeats     int      `json:"maxRepeats,omitempty"`
	MaxDelta       string   `json:"maxDelta,omitempty"`
	Slots          []string `json:"slots"`
}

type jsonSkipped struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type jsonSummary struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}

func hexBytes(b []byte) string {
	return "0x" + hex.EncodeToString(b)
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

func dependencyWord(n int) string {
	if n == 1 {
		return "dependency"
	}
	return "dependencies"
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

func formatSlots(slots []trace.Slot) string {
	return join(formatSlotList(slots))
}

func formatSlotList(slots []trace.Slot) []string {
	out := make([]string, len(slots))
	for i, s := range slots {
		out[i] = formatSlot(s)
	}
	return out
}

// formatSlot trims leading zero bytes so a simple slot prints as 0x02 rather than
// 64 hex characters.
func formatSlot(s trace.Slot) string {
	b := s[:]
	i := 0
	for i < len(b)-1 && b[i] == 0 {
		i++
	}
	return "0x" + hex.EncodeToString(b[i:])
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
