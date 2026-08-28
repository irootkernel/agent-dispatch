package hermeskanban

import (
	"fmt"
	"strings"
)

// SkillRow is one parsed skill-table row (SEC-014): the public
// profile-scoped `skills list` human table is the only skill surface,
// and this fail-closed parser is its frozen contract.
type SkillRow struct {
	Name     string
	Category string
	Source   string
	Trust    string
	Status   string
}

// Enabled reports whether the row's status is the enabled state.
func (s SkillRow) Enabled() bool { return s.Status == "enabled" }

// ParseSkillTable parses the rendered skill table fail-closed
// (SEC-014): exactly the five documented columns (Name, Category,
// Source, Trust, Status) in that order, box-drawn rows, one skill per
// row. Anything else — a different column set, a wrapped cell, a
// truncated table — is an error naming the defect, never a silent
// partial inventory (HER-014).
func ParseSkillTable(output string) ([]SkillRow, error) {
	lines := strings.Split(output, "\n")
	var header []string
	var rows []SkillRow
	for _, line := range lines {
		if !strings.ContainsAny(line, "│|┃") {
			continue
		}
		cells := splitTableRow(line)
		if len(cells) == 0 {
			continue
		}
		if header == nil {
			normalized := make([]string, len(cells))
			for i, c := range cells {
				normalized[i] = strings.ToLower(strings.TrimSpace(c))
			}
			if strings.Join(normalized, ",") != "name,category,source,trust,status" {
				return nil, fmt.Errorf("skill table header %q is not the documented five columns (name, category, source, trust, status)", strings.Join(cells, ", "))
			}
			header = normalized
			continue
		}
		if len(cells) != len(header) {
			return nil, fmt.Errorf("skill table row has %d cells, want %d (wrapped or truncated rows fail closed)", len(cells), len(header))
		}
		row := SkillRow{
			Name:     strings.TrimSpace(cells[0]),
			Category: strings.TrimSpace(cells[1]),
			Source:   strings.TrimSpace(cells[2]),
			Trust:    strings.TrimSpace(cells[3]),
			Status:   strings.TrimSpace(cells[4]),
		}
		if row.Name == "" {
			return nil, fmt.Errorf("skill table row with an empty name fails closed")
		}
		switch row.Status {
		case "enabled", "disabled":
		default:
			return nil, fmt.Errorf("skill %q has unknown status %q (want enabled or disabled)", row.Name, row.Status)
		}
		rows = append(rows, row)
	}
	if header == nil {
		return nil, fmt.Errorf("no skill table header found; the rendering environment or the public surface drifted")
	}
	return rows, nil
}

// splitTableRow splits one box-drawn table line into its cell texts,
// skipping rule rows (header separators) that carry no letters. The
// heavy header borders (┃) and the light data borders (│) are the same
// separator to the parser.
func splitTableRow(line string) []string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(line, "┃", "│"))
	if trimmed == "" || !strings.Contains(trimmed, "│") {
		return nil
	}
	// Rule rows consist of box-drawing and dashes/columns only.
	stripped := strings.Map(func(r rune) rune {
		switch r {
		case '─', '━', '┄', '┈', '╌', '┉', '┅', '│', '┃', '╂', '┇', '┊', '║', '┆', '┋', '┾', '┽', '╀', '╁', '┿', '┼', '╪', '╫', '╬', '-', '=', '+', ' ', '\t', '╄', '╆', '╅', '╇', '╈', '╉', '╊':
			return -1
		}
		return r
	}, trimmed)
	if stripped == "" {
		return nil
	}
	parts := strings.Split(trimmed, "│")
	if len(parts) < 2 {
		return nil
	}
	// The split leaves empty edges before the leading and after the
	// trailing separator.
	return parts[1 : len(parts)-1]
}
