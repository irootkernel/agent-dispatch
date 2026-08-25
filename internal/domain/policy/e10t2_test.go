package policy

import "testing"

// TestE10T2ExclusionFormsConfiguredRootRelative pins PTH-009's exclusion
// vocabulary — exact files, file globs, exact directories, recursive
// directories, and multiple simultaneous exclusions — all evaluated
// against configured-root-relative paths before any read.
func TestE10T2ExclusionFormsConfiguredRootRelative(t *testing.T) {
	engine, err := NewEngine(
		[]string{"**/*.md"},
		[]string{"Secrets", "Drafts/**", "Inbox/exact.md", "Inbox/noise-*.md"},
		nil, nil, CaseSensitive,
	)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want Status
		why  string
	}{
		{"Inbox/keep.md", StatusNormal, "in-scope file stays normal"},
		{"Secrets/a.md", StatusExcluded, "exact-directory exclusion covers the subtree"},
		{"Secrets/deep/nested/b.md", StatusExcluded, "exact-directory exclusion is recursive"},
		{"Drafts/wip.md", StatusExcluded, "recursive-directory exclusion"},
		{"Inbox/exact.md", StatusExcluded, "exact-file exclusion"},
		{"Sub/Inbox/exact.md", StatusNormal, "exact-file exclusion is root-relative, not name-global"},
		{"Inbox/noise-1.md", StatusExcluded, "file-glob exclusion"},
		{"Inbox/noise-abc.md", StatusExcluded, "file-glob exclusion matches runs"},
		{"Sub/Secrets/x.md", StatusNormal, "exact-directory exclusion is root-relative"},
	}
	for _, tc := range cases {
		got, err := engine.Classify(tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if got != tc.want {
			t.Errorf("%s: %s (want %s)", tc.why, tc.path, tc.want)
		}
	}
}

// TestE10T2ExclusionRunsBeforeInclude pins the evaluation order: an
// excluded path is excluded even when an include pattern would match it,
// and multiple exclusion forms compose.
func TestE10T2ExclusionRunsBeforeInclude(t *testing.T) {
	engine, err := NewEngine(
		[]string{"**/*"},
		[]string{"vendor", "**/node_modules/**", "*.tmp"},
		nil, nil, CaseSensitive,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"vendor/lib.js", "app/node_modules/pkg/index.js", "scratch.tmp"} {
		if got, _ := engine.Classify(path); got != StatusExcluded {
			t.Errorf("%s must be excluded by the composed forms, got %s", path, got)
		}
	}
	if got, _ := engine.Classify("app/main.js"); got != StatusNormal {
		t.Errorf("an ordinary path must stay normal, got %s", got)
	}
}

// TestE10T2DirectoryExclusionBoundaries pins the round-1 review
// boundary set: a file-pattern exclusion also covers a same-named
// directory subtree, a glob exclusion covers matching directory names,
// the recursive form keeps needing its explicit segments, and an
// include-only match never excludes.
func TestE10T2DirectoryExclusionBoundaries(t *testing.T) {
	engine, err := NewEngine([]string{"**/*"}, []string{"Inbox/exact.md", "*.tmp", "Drafts/**"}, nil, nil, CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want Status
	}{
		{"Inbox/exact.md", StatusExcluded},          // the exact file
		{"Inbox/exact.md/child.md", StatusExcluded}, // a directory the file-pattern names covers its subtree
		{"x.tmp", StatusExcluded},                   // the glob file form
		{"x.tmp/nested.md", StatusExcluded},         // a directory matching the glob covers its subtree
		{"Drafts/a.md", StatusExcluded},             // the recursive directory form
		{"Sub/Drafts/a.md", StatusNormal},           // root-relative: not the configured Drafts
		{"Inbox/keep.md", StatusNormal},             // an un-excluded sibling stays normal
	}
	for _, tc := range cases {
		if got, _ := engine.Classify(tc.path); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.path, got, tc.want)
		}
	}
}
