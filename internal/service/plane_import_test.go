package service

// Plane-native export importer tests (C14T1): input parsing + shape
// validation. Pure parse tests — no database, no writes.

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readPlaneFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return data
}

// zipOne builds an in-memory zip from name→content entries.
func zipBytes(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestParsePlaneImport_HappyPath(t *testing.T) {
	p, err := ParsePlaneImport(bytes.NewReader(readPlaneFixture(t, "plane-export.json")))
	if err != nil {
		t.Fatalf("ParsePlaneImport: %v", err)
	}
	if len(p.Errors) != 0 {
		t.Fatalf("expected no row errors, got %v", p.Errors)
	}
	if len(p.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(p.Rows))
	}
	if p.ProjectIdentifier != "ACME" {
		t.Errorf("ProjectIdentifier = %q, want ACME", p.ProjectIdentifier)
	}
	if p.ProjectName != "Acme Mobile" {
		t.Errorf("ProjectName = %q, want Acme Mobile", p.ProjectName)
	}

	r0 := p.Rows[0]
	if r0.Identifier != "ACME-42" {
		t.Errorf("row0 identifier = %q, want ACME-42", r0.Identifier)
	}
	if r0.Name != "Login button misaligned on mobile Safari" {
		t.Errorf("row0 name = %q", r0.Name)
	}
	if r0.SequenceID != 42 {
		t.Errorf("row0 sequence_id = %d, want 42", r0.SequenceID)
	}
	if string(r0.Estimate) != "3" {
		t.Errorf("row0 estimate = %s, want 3", r0.Estimate)
	}
	if len(r0.Comments) != 1 || r0.Comments[0].CreatedBy != "Ada Lovelace" {
		t.Errorf("row0 comments = %+v, want 1 comment by Ada Lovelace", r0.Comments)
	}
	if len(r0.Relations) != 1 || r0.Relations[0].Issue != "ACME-43" {
		t.Errorf("row0 relations = %+v, want 1 outgoing to ACME-43", r0.Relations)
	}
	if r0.Parent != "" {
		t.Errorf("row0 parent = %q, want empty", r0.Parent)
	}

	r1 := p.Rows[1]
	if r1.Identifier != "ACME-43" {
		t.Errorf("row1 identifier = %q, want ACME-43", r1.Identifier)
	}
	if r1.Parent != "ACME-42" {
		t.Errorf("row1 parent = %q, want ACME-42", r1.Parent)
	}
	// estimate "" (empty string, not null) must survive decoding.
	if string(r1.Estimate) != `""` {
		t.Errorf("row1 estimate = %s, want empty-string JSON", r1.Estimate)
	}

	proj, seq, err := ParsePlaneIdentifier("ACME-42")
	if err != nil {
		t.Fatalf("ParsePlaneIdentifier(ACME-42): %v", err)
	}
	if proj != "ACME" || seq != 42 {
		t.Errorf("ParsePlaneIdentifier(ACME-42) = (%q, %d), want (ACME, 42)", proj, seq)
	}
}

func TestParsePlaneImport_Zip(t *testing.T) {
	fixture := readPlaneFixture(t, "plane-export.json")
	p, err := ParsePlaneImport(bytes.NewReader(zipBytes(t, map[string][]byte{
		"plane-export.json": fixture,
	})))
	if err != nil {
		t.Fatalf("ParsePlaneImport(zip): %v", err)
	}
	if len(p.Rows) != 2 || len(p.Errors) != 0 {
		t.Fatalf("zip parse: got %d rows, %d errors", len(p.Rows), len(p.Errors))
	}
	if p.ProjectIdentifier != "ACME" {
		t.Errorf("ProjectIdentifier = %q, want ACME", p.ProjectIdentifier)
	}
}

func TestParsePlaneImport_ZipNoJSON(t *testing.T) {
	_, err := ParsePlaneImport(bytes.NewReader(zipBytes(t, map[string][]byte{
		"readme.txt": []byte("nothing to see here"),
	})))
	if err == nil || !strings.Contains(err.Error(), ".json") {
		t.Fatalf("expected .json-entry error, got %v", err)
	}
}

func TestParsePlaneImport_ZipTwoJSON(t *testing.T) {
	_, err := ParsePlaneImport(bytes.NewReader(zipBytes(t, map[string][]byte{
		"a.json": []byte("[]"),
		"b.json": []byte("[]"),
	})))
	if err == nil || !strings.Contains(err.Error(), "single") {
		t.Fatalf("expected single-.json error, got %v", err)
	}
}

func TestParsePlaneImport_ZipTruncated(t *testing.T) {
	full := zipBytes(t, map[string][]byte{"plane-export.json": []byte("[]")})
	_, err := ParsePlaneImport(bytes.NewReader(full[:len(full)/2]))
	if err == nil {
		t.Fatal("expected error for truncated zip, got nil")
	}
}

func TestParsePlaneImport_MalformedJSON(t *testing.T) {
	p, err := ParsePlaneImport(strings.NewReader(`[{"identifier": "ACME-1", `))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if p != nil {
		t.Fatalf("expected nil result on malformed JSON, got %+v", p)
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error should mention JSON, got: %v", err)
	}
}

func TestParsePlaneImport_TopLevelObject(t *testing.T) {
	_, err := ParsePlaneImport(strings.NewReader(`{"issues": []}`))
	if !errors.Is(err, ErrPlaneImportShape) {
		t.Fatalf("expected ErrPlaneImportShape, got %v", err)
	}
}

func TestParsePlaneImport_EmptyArray(t *testing.T) {
	_, err := ParsePlaneImport(strings.NewReader(`[]`))
	if !errors.Is(err, ErrPlaneImportEmpty) {
		t.Fatalf("expected ErrPlaneImportEmpty, got %v", err)
	}
}

func TestParsePlaneImport_EmptyFile(t *testing.T) {
	_, err := ParsePlaneImport(strings.NewReader(``))
	if !errors.Is(err, ErrPlaneImportEmpty) {
		t.Fatalf("expected ErrPlaneImportEmpty, got %v", err)
	}
}

func TestParsePlaneImport_NonJSONGarbage(t *testing.T) {
	_, err := ParsePlaneImport(strings.NewReader(`this is not json at all`))
	if err == nil {
		t.Fatal("expected error for non-JSON input, got nil")
	}
}

func TestParsePlaneImport_HostileRows(t *testing.T) {
	p, err := ParsePlaneImport(bytes.NewReader(readPlaneFixture(t, "plane-export-hostile.json")))
	if err != nil {
		t.Fatalf("ParsePlaneImport(hostile): %v", err)
	}
	// 3 valid rows: ACME-100 (control), ACME-103 (bad date tolerated at
	// parse level), ACME-104 (duplicate name tolerated).
	if len(p.Rows) != 3 {
		t.Fatalf("expected 3 valid rows, got %d: %+v", len(p.Rows), p.Rows)
	}
	gotIDs := map[string]bool{}
	for _, r := range p.Rows {
		gotIDs[r.Identifier] = true
	}
	for _, want := range []string{"ACME-100", "ACME-103", "ACME-104"} {
		if !gotIDs[want] {
			t.Errorf("expected valid row %s, got %v", want, gotIDs)
		}
	}
	// Bad date is a T3 validation concern; parse must keep the raw value.
	for _, r := range p.Rows {
		if r.Identifier == "ACME-103" {
			if r.StartDate == nil || *r.StartDate != "not-a-date" {
				t.Errorf("ACME-103 start_date not preserved: %+v", r.StartDate)
			}
		}
	}

	// 4 per-row errors: row-2 (missing identifier), ACME-101 (empty name),
	// ACME-102 (name has wrong type), acme-106 (lowercase identifier).
	if len(p.Errors) != 4 {
		t.Fatalf("expected 4 row errors, got %d: %+v", len(p.Errors), p.Errors)
	}
	errByID := map[string]string{}
	for _, e := range p.Errors {
		if e.Message == "" {
			t.Errorf("row error %q has empty message", e.Identifier)
		}
		errByID[e.Identifier] = e.Message
	}
	for _, want := range []string{"row-2", "ACME-101", "ACME-102", "acme-106"} {
		if _, ok := errByID[want]; !ok {
			t.Errorf("expected row error keyed %q, got keys %v", want, keysOf(errByID))
		}
	}
}

func keysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestParsePlaneImport_MultiProject(t *testing.T) {
	body := `[
		{"identifier":"ACME-1","project_identifier":"ACME","project_name":"Acme","name":"one","sequence_id":1},
		{"identifier":"ACME-2","project_identifier":"ACME","project_name":"Acme","name":"two","sequence_id":2},
		{"identifier":"BETA-1","project_identifier":"BETA","project_name":"Beta","name":"three","sequence_id":1}
	]`
	_, err := ParsePlaneImport(strings.NewReader(body))
	if !errors.Is(err, ErrPlaneImportMultiProject) {
		t.Fatalf("expected ErrPlaneImportMultiProject, got %v", err)
	}
	if !strings.Contains(err.Error(), "BETA") {
		t.Errorf("error should list the extra project, got: %v", err)
	}
}

func TestParsePlaneImport_UnknownFieldsTolerated(t *testing.T) {
	body := `[
		{"identifier":"ACME-1","project_identifier":"ACME","name":"one","sequence_id":1,
		 "some_future_field":{"nested":[1,2,3]},"another_one":"x"}
	]`
	p, err := ParsePlaneImport(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParsePlaneImport: %v", err)
	}
	if len(p.Rows) != 1 || len(p.Errors) != 0 {
		t.Fatalf("expected 1 row, 0 errors; got %d rows, %d errors", len(p.Rows), len(p.Errors))
	}
}

func TestParsePlaneImport_DuplicateNamesAllowed(t *testing.T) {
	// Duplicate names are legal (flagged by nobody at parse time); the
	// hostile fixture already covers this, this is the explicit contract.
	p, err := ParsePlaneImport(bytes.NewReader(readPlaneFixture(t, "plane-export-hostile.json")))
	if err != nil {
		t.Fatalf("ParsePlaneImport: %v", err)
	}
	names := map[string]int{}
	for _, r := range p.Rows {
		names[r.Name]++
	}
	if names["Duplicate name row"] != 2 {
		t.Errorf("expected 2 rows named %q, got %d", "Duplicate name row", names["Duplicate name row"])
	}
}

func TestParsePlaneIdentifier(t *testing.T) {
	cases := []struct {
		in      string
		project string
		seq     int
		ok      bool
	}{
		{"ACME-42", "ACME", 42, true},
		{"AC-ME-7", "AC-ME", 7, true}, // dash in project part: split on LAST dash
		{"A-1", "A", 1, true},
		{"ABC-007", "ABC", 7, true},
		{"", "", 0, false},
		{"ACME", "", 0, false},
		{"ACME-", "", 0, false},
		{"-42", "", 0, false},
		{"acme-42", "", 0, false}, // lowercase rejected
		{"ACME-4a", "", 0, false}, // non-numeric sequence
		{"ACME--1", "", 0, false}, // empty dash segment
		{"-ACME-1", "", 0, false}, // leading dash
		{"ACME-1-2", "ACME-1", 2, true},
	}
	for _, c := range cases {
		proj, seq, err := ParsePlaneIdentifier(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("ParsePlaneIdentifier(%q): unexpected error %v", c.in, err)
				continue
			}
			if proj != c.project || seq != c.seq {
				t.Errorf("ParsePlaneIdentifier(%q) = (%q, %d), want (%q, %d)",
					c.in, proj, seq, c.project, c.seq)
			}
		} else if err == nil {
			t.Errorf("ParsePlaneIdentifier(%q): expected error, got (%q, %d)", c.in, proj, seq)
		}
	}
}

func TestParsePlaneImport_BOMStripped(t *testing.T) {
	data := append([]byte("\xef\xbb\xbf"), readPlaneFixture(t, "plane-export.json")...)
	p, err := ParsePlaneImport(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ParsePlaneImport(BOM): %v", err)
	}
	if len(p.Rows) != 2 {
		t.Fatalf("expected 2 rows after BOM strip, got %d", len(p.Rows))
	}
}
