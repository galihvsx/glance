package testdb

import "testing"

func TestPackageDBName(t *testing.T) {
	tests := []struct {
		base, name, want string
		wantErr          bool
	}{
		{"glance_test", "mail", "glance_test_mail", false},
		{"glance_test", "Mail", "glance_test_mail", false}, // lowercased
		{"glance_test", "API", "glance_test_api", false},   // lowercased
		{"glance_test", "svc_2", "glance_test_svc_2", false},
		{"glance_test", "", "", true},          // empty name rejected
		{"glance_test", "my-pkg", "", true},    // dash rejected
		{"glance_test", "my pkg", "", true},    // space rejected
		{"glance_test", "a/b", "", true},       // slash rejected
		{"glance_test", `x";DROP`, "", true},   // injection rejected
		{"glance_test", "ünïcodé", "", true},   // non-ascii rejected
		{"", "mail", "", true},                 // empty base rejected
		{"glance-test", "mail", "", true},      // unsafe base rejected
		{"glance_test;DROP", "mail", "", true}, // unsafe base rejected
	}
	for _, tc := range tests {
		got, err := packageDBName(tc.base, tc.name)
		if tc.wantErr {
			if err == nil {
				t.Errorf("packageDBName(%q, %q) = %q, want error", tc.base, tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("packageDBName(%q, %q) error: %v", tc.base, tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("packageDBName(%q, %q) = %q, want %q", tc.base, tc.name, got, tc.want)
		}
	}
}

func TestWithDBNamePreservesURLParts(t *testing.T) {
	raw := "postgres://u:pw@127.0.0.1:5432/glance_test?sslmode=disable"
	got, err := withDBName(raw, "glance_test_mail")
	if err != nil {
		t.Fatalf("withDBName: %v", err)
	}
	want := "postgres://u:pw@127.0.0.1:5432/glance_test_mail?sslmode=disable"
	if got != want {
		t.Errorf("withDBName = %q, want %q", got, want)
	}
}

func TestWithDBNameKeepsUserinfoAndQuery(t *testing.T) {
	raw := "postgres://user:p%40ss@db.example.com:5433/other?sslmode=require&connect_timeout=5"
	got, err := withDBName(raw, "other_auth")
	if err != nil {
		t.Fatalf("withDBName: %v", err)
	}
	want := "postgres://user:p%40ss@db.example.com:5433/other_auth?sslmode=require&connect_timeout=5"
	if got != want {
		t.Errorf("withDBName = %q, want %q", got, want)
	}
}

func TestWithDBNameInvalidURL(t *testing.T) {
	if _, err := withDBName("://missing-scheme", "x"); err == nil {
		t.Error("withDBName on invalid URL: got nil error, want error")
	}
}
