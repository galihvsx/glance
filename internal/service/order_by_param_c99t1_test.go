package service

// Multi-key ?order_by= param parsing (C99T1): the param resolves
// through the same listOrders whitelist as the single-key path, one
// ord per comma-separated key, reusing the T0 composite-cursor
// machinery in ListIssues. These are pure parser tests (no DB):
// the HTTP tests in internal/api prove the order actually reorders
// rows and paginates.

import (
	"errors"
	"testing"
)

func TestResolveParamOrderBy(t *testing.T) {
	// Multi-key: columns and directions resolve per key, in order.
	ords, err := resolveParamOrderBy("priority,-updated_at")
	if err != nil {
		t.Fatalf("multi-key: err = %v", err)
	}
	if len(ords) != 2 {
		t.Fatalf("multi-key: got %d ords, want 2", len(ords))
	}
	if ords[0].column != "i.priority" || ords[0].desc {
		t.Fatalf("multi-key key 1: got %+v, want i.priority ASC", ords[0])
	}
	if ords[1].column != "i.updated_at" || !ords[1].desc {
		t.Fatalf("multi-key key 2: got %+v, want i.updated_at DESC", ords[1])
	}

	// Single key stays a single ord (byte-identical path).
	ords, err = resolveParamOrderBy("-updated_at")
	if err != nil || len(ords) != 1 || ords[0].column != "i.updated_at" || !ords[0].desc {
		t.Fatalf("single-key: ords = %v err = %v, want one i.updated_at DESC", ords, err)
	}

	// Every whitelist key resolves, bare and "-"-prefixed.
	for key, want := range listOrders {
		ords, err := resolveParamOrderBy(key)
		if err != nil || len(ords) != 1 || ords[0] != want {
			t.Fatalf("whitelist %q: ords = %v err = %v, want %+v", key, ords, err, want)
		}
	}

	// Unknown key, empty segment, and duplicate key are all
	// ErrInvalidOrderBy — never a silently-dropped key.
	bad := map[string]string{
		"unknown key":        "bogus",
		"unknown in list":    "priority,bogus",
		"double comma":       "priority,,sequence_id",
		"leading comma":      ",priority",
		"trailing comma":     "priority,",
		"only comma":         ",",
		"empty":              "",
		"duplicate exact":    "priority,priority",
		"duplicate flipped":  "priority,-priority",
		"duplicate reversed": "-priority,priority",
		"ASC/DESC words":     "priority DESC",
		"whitespace segment": "priority, -updated_at",
	}
	for name, v := range bad {
		if _, err := resolveParamOrderBy(v); !errors.Is(err, ErrInvalidOrderBy) {
			t.Fatalf("%s (%q): err = %v, want ErrInvalidOrderBy", name, v, err)
		}
	}
}
