package report

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/Hijanhv/Sequent/internal/analyze"
	"github.com/Hijanhv/Sequent/internal/graph"
	"github.com/Hijanhv/Sequent/internal/trace"
)

func word(n int64) []byte {
	b := make([]byte, 32)
	big.NewInt(n).FillBytes(b)
	return b
}

func sampleReport(t *testing.T) Report {
	t.Helper()
	impacts := []analyze.Impact{
		{Writer: "w1()", Reader: "r1()", Confirmed: true, Before: word(0), After: word(5), Delta: big.NewInt(5)},
		{Writer: "w2()", Reader: "r2()", Confirmed: true, BeforeReverted: true, AfterReverted: false},
		{Writer: "w3()", Reader: "r3()", Confirmed: false},
		{Writer: "w4()", Reader: "r4()", Confirmed: true, Before: word(0), After: word(2), Delta: big.NewInt(2)},
		{Writer: "w5()", Reader: "r5()", Confirmed: true, Before: []byte{0xaa}, After: []byte{0xbb}},
	}
	g := graph.Graph{Edges: []graph.Edge{
		{Writer: "w1()", Reader: "r1()", Slots: []trace.Slot{trace.SlotFromUint64(1)}},
		{Writer: "w2()", Reader: "r2()", Slots: []trace.Slot{trace.SlotFromUint64(2)}},
		{Writer: "w3()", Reader: "r3()", Slots: []trace.Slot{trace.SlotFromUint64(3)}},
		{Writer: "w4()", Reader: "r4()", Slots: []trace.Slot{trace.SlotFromUint64(4)}},
		{Writer: "w5()", Reader: "r5()", Slots: []trace.Slot{trace.SlotFromUint64(5)}},
	}}
	return Build("Vault.json", 8, nil, g, impacts)
}

func TestBuildSeverityCountsAndRanking(t *testing.T) {
	r := sampleReport(t)

	if r.High != 3 || r.Medium != 1 || r.Low != 1 {
		t.Fatalf("counts = high %d, medium %d, low %d; want 3/1/1", r.High, r.Medium, r.Low)
	}

	// High by magnitude (5, then 2, then the revert flip at 0), then medium, then low.
	wantOrder := []string{"w1()", "w4()", "w2()", "w5()", "w3()"}
	for i, w := range wantOrder {
		if r.Findings[i].Writer != w {
			t.Fatalf("finding %d writer = %s, want %s (order: %v)", i, r.Findings[i].Writer, w, writers(r.Findings))
		}
	}
}

func TestBuildEffectClassification(t *testing.T) {
	r := sampleReport(t)
	byWriter := make(map[string]Finding)
	for _, f := range r.Findings {
		byWriter[f.Writer] = f
	}
	cases := map[string]string{
		"w1()": "value-change",
		"w2()": "revert-flip",
		"w3()": "shared",
		"w5()": "output-change",
	}
	for writer, want := range cases {
		if got := byWriter[writer].Effect; got != want {
			t.Fatalf("%s effect = %s, want %s", writer, got, want)
		}
	}
}

func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	sampleReport(t).WriteText(&buf)
	out := buf.String()

	wants := []string{
		"w1() -> r1()   reader value 0 -> 5 (change +5)",
		"w2() -> r2()   reverted -> succeeded",
		"w5() -> r5()   reader output 0xaa -> 0xbb",
		"w3() -> r3()   shares slots 0x03 (no effect observed)",
		"Summary: 3 high, 1 medium, 1 low (of 5 dependencies).",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Fatalf("text output missing %q; got:\n%s", w, out)
		}
	}
}

func TestWriteTextNoFindings(t *testing.T) {
	var buf bytes.Buffer
	Build("Empty.json", 2, nil, graph.Graph{}, nil).WriteText(&buf)
	if !strings.Contains(buf.String(), "No ordering dependencies found") {
		t.Fatalf("expected the no-dependencies message, got:\n%s", buf.String())
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := sampleReport(t).WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var got struct {
		Contract          string `json:"contract"`
		FunctionsAnalyzed int    `json:"functionsAnalyzed"`
		Findings          []struct {
			Severity string `json:"severity"`
			Writer   string `json:"writer"`
			Effect   string `json:"effect"`
			Delta    string `json:"delta"`
		} `json:"findings"`
		Summary struct {
			High   int `json:"high"`
			Medium int `json:"medium"`
			Low    int `json:"low"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\njson:\n%s", err, buf.String())
	}

	if got.Contract != "Vault.json" || got.FunctionsAnalyzed != 8 {
		t.Fatalf("header = %s / %d", got.Contract, got.FunctionsAnalyzed)
	}
	if got.Summary.High != 3 || got.Summary.Medium != 1 || got.Summary.Low != 1 {
		t.Fatalf("summary = %+v", got.Summary)
	}
	if len(got.Findings) != 5 {
		t.Fatalf("expected 5 findings, got %d", len(got.Findings))
	}
	if got.Findings[0].Severity != "HIGH" || got.Findings[0].Delta != "5" || got.Findings[0].Effect != "value-change" {
		t.Fatalf("top finding = %+v", got.Findings[0])
	}
}

func writers(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Writer
	}
	return out
}
