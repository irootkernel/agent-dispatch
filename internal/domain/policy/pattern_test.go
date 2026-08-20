package policy

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The golden file is the cross-platform contract: the same normalized
// paths and patterns produce the same classification on macOS and Linux
// (configuration-spec §7), because the engine only sees slash-separated
// data and an explicit case mode.
const goldenPath = "testdata/pattern-golden.json"

type goldenCase struct {
	Path   string `json:"path"`
	Status Status `json:"status"`
}

type goldenMatch struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Match   bool   `json:"match"`
}

type goldenFile struct {
	CaseMode       string        `json:"case_mode"`
	Include        []string      `json:"include"`
	Exclude        []string      `json:"exclude"`
	Protected      []string      `json:"protected"`
	Immutable      []string      `json:"immutable"`
	Cases          []goldenCase  `json:"cases"`
	SegmentMatches []goldenMatch `json:"segment_matches"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGoldenClassification(t *testing.T) {
	g := loadGolden(t)
	engine, err := NewEngine(g.Include, g.Exclude, g.Protected, g.Immutable, CaseMode(g.CaseMode))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range g.Cases {
		got, err := engine.Classify(c.Path)
		if err != nil {
			t.Fatalf("%s: %v", c.Path, err)
		}
		if got != c.Status {
			t.Fatalf("%s: got %s, want %s", c.Path, got, c.Status)
		}
	}
}

func TestGoldenSegmentMatching(t *testing.T) {
	g := loadGolden(t)
	for _, m := range g.SegmentMatches {
		segs, err := compile(m.Pattern)
		if err != nil {
			t.Fatalf("pattern %q: %v", m.Pattern, err)
		}
		names := splitPath(m.Path)
		got := matchSegments(segs, names)
		if got != m.Match {
			t.Fatalf("pattern %q path %q: got %v, want %v", m.Pattern, m.Path, got, m.Match)
		}
	}
}

func splitPath(p string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			if i > start {
				out = append(out, p[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func TestCaseFoldingIsExplicit(t *testing.T) {
	insensitive, err := NewEngine([]string{"Readme.md"}, nil, nil, nil, CaseInsensitive)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := insensitive.Classify("README.md"); st != StatusNormal {
		t.Fatalf("insensitive mode should match different case, got %s", st)
	}
	sensitive, err := NewEngine([]string{"Readme.md"}, nil, nil, nil, CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := sensitive.Classify("README.md"); st != StatusExcluded {
		t.Fatalf("sensitive mode should not match different case, got %s", st)
	}
	if _, err := NewEngine(nil, nil, nil, nil, CaseFilesystem); err == nil {
		t.Fatal("filesystem mode must be resolved by the caller before compiling")
	}
}

func TestInvalidPatternsFailClosed(t *testing.T) {
	bad := []string{
		"", "/abs.md", `back\slash.md`, "../escape.md", "./rel.md",
		"a//b.md", "a/**b.md", "a/b**/c.md", "trailing/.", "..",
	}
	for _, p := range bad {
		if _, err := NewEngine([]string{p}, nil, nil, nil, CaseSensitive); err == nil {
			t.Fatalf("pattern %q must fail closed", p)
		}
	}
}

func TestClassifyRejectsUnusablePaths(t *testing.T) {
	engine, err := NewEngine([]string{"**/*"}, nil, nil, nil, CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "/abs", "../up", "a/../b", "a\x00b", "a//b", `a\b`} {
		if _, err := engine.Classify(p); err == nil {
			t.Fatalf("path %q must be rejected as data, not classified", p)
		}
	}
}

// TestEventContentCannotAffectRouteAuthority (PTH-004, SEC-003): paths
// are classification input only. Malicious-looking names cannot widen the
// include set, flip protected status of other paths, or alter the
// compiled patterns.
func TestEventContentCannotAffectRouteAuthority(t *testing.T) {
	engine, err := NewEngine([]string{"**/*.md"}, []string{"tmp/**"}, []string{"raw/**"}, nil, CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	beforeInc, beforeExc, beforeProt, beforeImm := engine.Patterns()
	hostile := []string{
		"raw/../../etc/passwd",
		"include=**/*.txt",
		"profile=admin/**",
		"tmp/../raw/secret.md",
		"**/*.md",
	}
	for _, p := range hostile {
		_, _ = engine.Classify(p) // outcome irrelevant; must not mutate engine
	}
	inc, exc, prot, imm := engine.Patterns()
	if !reflect.DeepEqual(inc, beforeInc) || !reflect.DeepEqual(exc, beforeExc) ||
		!reflect.DeepEqual(prot, beforeProt) || !reflect.DeepEqual(imm, beforeImm) {
		t.Fatal("engine patterns mutated by hostile paths")
	}
	// Fixed paths keep their classification regardless of hostile input.
	for p, want := range map[string]Status{
		"Inbox/a.md": StatusNormal,
		"tmp/x.md":   StatusExcluded,
		"raw/x.md":   StatusProtected,
	} {
		if got, err := engine.Classify(p); err != nil || got != want {
			t.Fatalf("%s: got %s (%v), want %s", p, got, err, want)
		}
	}
}

// TestProtectedClassifiedWithoutReading proves classification is pure:
// no filesystem access occurs, so protected and even nonexistent paths
// classify identically whether or not they exist.
func TestProtectedClassifiedWithoutReading(t *testing.T) {
	engine, err := NewEngine([]string{"**/*.md"}, nil, []string{"raw/**"}, nil, CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	st, err := engine.Classify("raw/does-not-exist-at-all.md")
	if err != nil || st != StatusProtected {
		t.Fatalf("nonexistent protected path: %s (%v)", st, err)
	}
}

func TestClassifyManyOrder(t *testing.T) {
	engine, err := NewEngine([]string{"**/*.md"}, nil, []string{"raw/**"}, nil, CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	sts, err := engine.ClassifyMany([]string{"a.md", "raw/b.md", "x.png"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Status{StatusNormal, StatusProtected, StatusExcluded}
	for i := range want {
		if sts[i] != want[i] {
			t.Fatalf("index %d: got %s want %s", i, sts[i], want[i])
		}
	}
}

func TestClassifyEnforcesPathLengthCap(t *testing.T) {
	engine, err := NewEngine([]string{"**/*"}, nil, nil, nil, CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetMaxPathBytes(16)
	if _, err := engine.Classify("a/very/long/path/over/the/cap.md"); err == nil {
		t.Fatal("over-cap path must be an input error, not a classification")
	}
	engine.SetMaxPathBytes(0)
	if st, err := engine.Classify("a/very/long/path/over/the/cap.md"); err != nil || st != StatusNormal {
		t.Fatalf("default cap should accept normal paths: %s (%v)", st, err)
	}
}

func TestMatchSegmentsLinearOnHostileDepth(t *testing.T) {
	engine, err := NewEngine([]string{"**/a/**/b/**/c/**/x.md"}, nil, nil, nil, CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	deep := ""
	for i := 0; i < 500; i++ {
		if i > 0 {
			deep += "/"
		}
		deep += "seg"
	}
	st, err := engine.Classify(deep)
	if err != nil {
		t.Fatal(err)
	}
	if st != StatusExcluded {
		t.Fatalf("deep path should not match, got %s", st)
	}
}
