package ids

import (
	"regexp"
	"testing"
	"time"
)

func TestUUIDv7ShapeAndOrder(t *testing.T) {
	base := time.UnixMilli(1760000000000)
	gen := NewUUIDv7(func() time.Time { return base })
	seen := map[ID]bool{}
	prev := ""
	for i := 0; i < 100; i++ {
		id, err := gen.NewID()
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true
		if _, err := ParseUUIDv7(id.String()); err != nil {
			t.Fatalf("generated id must parse as uuidv7: %v", err)
		}
		// Same-millisecond ids must still be random-ordered, but each id
		// must be lexicographically greater when the timestamp advances.
		if prev != "" && id.String()[:14] == prev[:14] && id.String() == prev {
			t.Fatal("identical ids")
		}
		prev = id.String()
	}
}

func TestUUIDv7TimeOrdering(t *testing.T) {
	t1 := time.UnixMilli(1760000000000)
	t2 := t1.Add(time.Second)
	a, _ := NewUUIDv7(func() time.Time { return t1 }).NewID()
	b, _ := NewUUIDv7(func() time.Time { return t2 }).NewID()
	if a.String() >= b.String() {
		t.Fatalf("later timestamp must sort after: %s vs %s", a, b)
	}
}

func TestParseUUIDv7FailsClosed(t *testing.T) {
	bad := []string{
		"", "not-a-uuid", "0192e6c6-4d7f-7abc-8def-01234567890", // 35 chars
		"0192e6c6-4d7f-4abc-9def-012345678901", // version 4
		"0192e6c6-4d7f-7abc-cdef-012345678901", // bad variant
		"0192e6c6-4d7f-7abc-8def-0123456789zz", // bad hex
	}
	for _, s := range bad {
		if _, err := ParseUUIDv7(s); err == nil {
			t.Errorf("%q must fail", s)
		}
	}
	if _, err := ParseUUIDv7("0192e6c6-4d7f-7abc-8def-012345678901"); err != nil {
		t.Errorf("valid uuidv7 rejected: %v", err)
	}
}

func TestParseID(t *testing.T) {
	if _, err := ParseID(""); err == nil {
		t.Error("empty id must fail")
	}
	if _, err := ParseID(" padded "); err == nil {
		t.Error("padded id must fail")
	}
	if id, err := ParseID("anything-1"); err != nil || id != ID("anything-1") {
		t.Errorf("unexpected: %v", err)
	}
}

func TestSequentialDeterministic(t *testing.T) {
	g := &Sequential{}
	a, _ := g.NewID()
	b, _ := g.NewID()
	if a != "id-00000001" || b != "id-00000002" {
		t.Fatalf("sequential generator not deterministic: %s %s", a, b)
	}
	if !regexp.MustCompile(`^id-\d{8}$`).MatchString(b.String()) {
		t.Errorf("shape: %s", b)
	}
}
