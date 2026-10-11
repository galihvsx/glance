package service

// C18T4 project icon tests: the single-emoji validation table
// (normalizeProjectIcon), empty→NULL normalization, and the icon
// round-trip through create/get/list/update. Real test database, no skips.
// Reuses the harness from workspace_test.go (newTestPool, migrateTestDB,
// createTestUser, createTestWorkspace, uniqueTestEmail, uniqueTestSlug).

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizeProjectIconTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// want == nil means NULL (unset).
		want    *string
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"whitespace only", "   ", nil, false},
		{"simple emoji", "🚀", strPtr("🚀"), false},
		{"surrounding whitespace trimmed", "  🚀  ", strPtr("🚀"), false},
		{"emoji with VS16", "☀️", strPtr("☀️"), false},
		{"skin tone modifier", "👍🏽", strPtr("👍🏽"), false},
		{"flag (regional pair)", "🇺🇸", strPtr("🇺🇸"), false},
		{"keycap", "1️⃣", strPtr("1️⃣"), false},
		{"bare keycap (no VS16)", "1⃣", strPtr("1⃣"), false},
		{"dingbat", "⚙", strPtr("⚙"), false},
		{"copyright", "©", strPtr("©"), false},
		{"zwj family rejected", "👨‍👩‍👧", nil, true},
		{"zwj profession rejected", "🧑‍💻", nil, true},
		{"two emoji rejected", "🚀🚀", nil, true},
		{"text rejected", "ENG", nil, true},
		{"letter rejected", "a", nil, true},
		{"digit rejected", "5", nil, true},
		{"lone regional indicator rejected", "🇺", nil, true},
		{"emoji plus letter rejected", "🚀x", nil, true},
		{"letter plus emoji rejected", "x🚀", nil, true},
		{"text presentation selector rejected", "☀︎", nil, true},
		{"long string rejected", "🚀🚀🚀🚀🚀", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeProjectIcon(tc.in)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidIcon) {
					t.Fatalf("normalizeProjectIcon(%q): err = %v, want ErrInvalidIcon", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeProjectIcon(%q): unexpected err %v", tc.in, err)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("normalizeProjectIcon(%q) = %q, want NULL", tc.in, *got)
				}
				return
			}
			if got == nil || *got != *tc.want {
				gotStr := "<nil>"
				if got != nil {
					gotStr = *got
				}
				t.Fatalf("normalizeProjectIcon(%q) = %q, want %q", tc.in, gotStr, *tc.want)
			}
		})
	}
}

func TestProjectIconRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("proj-icon"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-icon"), creator)

	// Create with an icon: stored and returned.
	icon := "🚀"
	p, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, creator, "Iconauts", uniqueTestIdentifier(), nil, &icon)
	if err != nil {
		t.Fatalf("create with icon: %v", err)
	}
	if p.Icon == nil || *p.Icon != "🚀" {
		t.Fatalf("create returned icon = %v, want 🚀", p.Icon)
	}

	// Get returns it.
	got, err := GetProject(ctx, pool, ws.Slug, p.Identifier, creator)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Icon == nil || *got.Icon != "🚀" {
		t.Fatalf("get icon = %v, want 🚀", got.Icon)
	}

	// List returns it.
	list, err := ListProjects(ctx, pool, ws.Slug, creator, false)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	var found *Project
	for i := range list {
		if list[i].ID == p.ID {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatalf("created project missing from list")
	}
	if found.Icon == nil || *found.Icon != "🚀" {
		t.Fatalf("list icon = %v, want 🚀", found.Icon)
	}

	// Patch changes it.
	target := "🎯"
	upd, err := UpdateProject(ctx, pool, ws.Slug, p.Identifier, creator, ProjectPatch{Icon: &target})
	if err != nil {
		t.Fatalf("patch icon: %v", err)
	}
	if upd.Icon == nil || *upd.Icon != "🎯" {
		t.Fatalf("patch returned icon = %v, want 🎯", upd.Icon)
	}

	// Explicit empty string clears it → NULL.
	empty := ""
	cleared, err := UpdateProject(ctx, pool, ws.Slug, p.Identifier, creator, ProjectPatch{Icon: &empty})
	if err != nil {
		t.Fatalf("clear icon: %v", err)
	}
	if cleared.Icon != nil {
		t.Fatalf("clear icon: got %q, want NULL", *cleared.Icon)
	}

	// Omitted icon leaves the field untouched (still NULL here).
	untouched, err := UpdateProject(ctx, pool, ws.Slug, p.Identifier, creator, ProjectPatch{Name: strPtr("Iconauts Two")})
	if err != nil {
		t.Fatalf("patch name-only: %v", err)
	}
	if untouched.Icon != nil {
		t.Fatalf("name-only patch changed icon to %q, want untouched NULL", *untouched.Icon)
	}

	// Invalid icon on patch → ErrInvalidIcon.
	bad := "not-an-emoji"
	if _, err := UpdateProject(ctx, pool, ws.Slug, p.Identifier, creator, ProjectPatch{Icon: &bad}); !errors.Is(err, ErrInvalidIcon) {
		t.Fatalf("patch bad icon: err = %v, want ErrInvalidIcon", err)
	}

	// Invalid icon on create → ErrInvalidIcon.
	if _, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, creator, "Bad Icon", uniqueTestIdentifier(), nil, &bad); !errors.Is(err, ErrInvalidIcon) {
		t.Fatalf("create bad icon: err = %v, want ErrInvalidIcon", err)
	}

	// Empty icon on create → NULL.
	created2, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, creator, "No Icon", uniqueTestIdentifier(), nil, &empty)
	if err != nil {
		t.Fatalf("create empty icon: %v", err)
	}
	if created2.Icon != nil {
		t.Fatalf("create empty icon: got %q, want NULL", *created2.Icon)
	}

	// Omitted icon on create → NULL.
	created3, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, creator, "Nil Icon", uniqueTestIdentifier(), nil, nil)
	if err != nil {
		t.Fatalf("create nil icon: %v", err)
	}
	if created3.Icon != nil {
		t.Fatalf("create nil icon: got %q, want NULL", *created3.Icon)
	}
}
