package service

// Plane-native export importer — T1: input parsing + shape validation
// (C14T1, cycle 14 "Plane-native import").
//
// Design choice: input is a Plane JSON export (the `json` provider), as
// raw `.json` or as a `.zip` containing a single `.json` (the shape Plane
// actually delivers for download). The first two bytes decide: `PK` = zip.
//
// Decoding is strict-ish per row: the whole file is first split into raw
// row payloads, then each row is decoded independently. Unknown extra
// fields are tolerated (Plane's column set drifts between versions); a
// wrong type in a row (e.g. "name": 123) is a PER-ROW error — it never
// fails the whole file. Per-row errors are keyed by Plane `identifier`
// (not row index), so callers can correlate them with the source issues.
//
// Shape validation (the whole-file contract, since a real Plane export
// carries no version envelope):
//   - top level must be a JSON array (bare array — Plane exports have no
//     envelope); anything else is ErrPlaneImportShape;
//   - every row: `identifier` non-empty, matching PROJECT-123 where the
//     project part may itself contain dashes (split on the LAST dash, so
//     AC-ME-7 -> project AC-ME, sequence 7);
//   - every row: `name` non-empty;
//   - every row: `project_identifier` non-empty, and ALL rows share one
//     project_identifier, else ErrPlaneImportMultiProject listing the
//     extras (one import run = one Plane project -> one glance project).
//
// No database writes happen here and no migration is involved — this task
// is migration-free by design. T2 (analysis) and T3 (two-phase execute)
// build on PlaneIssueRow / PlaneImportRowError / ParsePlaneIdentifier.
//
// Safety: input is capped at planeImportMaxBytes (256 MiB) on both the
// raw stream and any zip entry.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// planeImportMaxBytes caps how much input ParsePlaneImport will read:
// 256 MiB covers any realistic issue export while bounding zip-bomb and
// memory abuse.
const planeImportMaxBytes = 256 << 20

var (
	// ErrPlaneImportShape is returned when the payload is valid input but
	// not a top-level JSON array (e.g. a JSON object, or a wrapped
	// envelope that a real Plane export never has).
	ErrPlaneImportShape = errors.New("service: plane export must be a top-level JSON array")
	// ErrPlaneImportEmpty is returned when the export parses but carries
	// no issues.
	ErrPlaneImportEmpty = errors.New("service: plane export has no issues")
	// ErrPlaneImportMultiProject is returned when rows carry more than one
	// project_identifier. The wrapped message lists the extra projects.
	ErrPlaneImportMultiProject = errors.New("service: plane export spans multiple projects")
)

// planeIdentifierProject matches the project part of a Plane identifier:
// uppercase alphanumerics, dash-separated segments (ACME, AC-ME).
var planeIdentifierProject = regexp.MustCompile(`^[A-Z0-9]+(-[A-Z0-9]+)*$`)

// planeIdentifierSeq matches the numeric sequence part.
var planeIdentifierSeq = regexp.MustCompile(`^[0-9]+$`)

// ParsePlaneIdentifier splits a Plane issue identifier on the LAST dash
// into its project part and sequence number: "ACME-42" -> ("ACME", 42),
// "AC-ME-7" -> ("AC-ME", 7). The project part is uppercase alphanumerics
// (dash-separated); the sequence is decimal digits.
func ParsePlaneIdentifier(id string) (project string, seq int, err error) {
	i := strings.LastIndex(id, "-")
	if i <= 0 || i == len(id)-1 {
		return "", 0, fmt.Errorf("invalid plane identifier %q (want PROJECT-123)", id)
	}
	project, seqStr := id[:i], id[i+1:]
	if !planeIdentifierProject.MatchString(project) {
		return "", 0, fmt.Errorf("invalid plane identifier %q (project part %q must be uppercase A-Z, 0-9, dashes)", id, project)
	}
	if !planeIdentifierSeq.MatchString(seqStr) {
		return "", 0, fmt.Errorf("invalid plane identifier %q (sequence %q must be digits)", id, seqStr)
	}
	n, err := strconv.Atoi(seqStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid plane identifier %q: %w", id, err)
	}
	return project, n, nil
}

// PlaneIssueComment is one Plane issue comment. created_at is naive
// ("2026-10-05 14:22:10"); T3 interprets it as UTC.
type PlaneIssueComment struct {
	Comment   string `json:"comment"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
}

// PlaneIssueLink is one Plane issue link.
type PlaneIssueLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// PlaneIssueRelation is one Plane issue-to-issue relation (glance has no
// issue relations; T3 reports these as import gaps, never inserts them).
type PlaneIssueRelation struct {
	Direction string `json:"direction"`
	Issue     string `json:"issue"`
	Type      string `json:"type"`
}

// PlaneIssueRow is one decoded Plane export row. Field types mirror the
// observed export shape: optional strings are *string (null-tolerant),
// Estimate is json.RawMessage because Plane emits it as a number OR an
// empty string in the same file. Wrong types for a field make THAT row
// fail decoding (per-row error), never the whole file.
type PlaneIssueRow struct {
	ArchivedAt        *string              `json:"archived_at"`
	Assignees         []string             `json:"assignees"`
	AttachmentCount   int                  `json:"attachment_count"`
	Comments          []PlaneIssueComment  `json:"comments"`
	CompletedAt       *string              `json:"completed_at"`
	CreatedAt         string               `json:"created_at"`
	CreatedByName     string               `json:"created_by_name"`
	Cycles            []string             `json:"cycles"`
	Estimate          json.RawMessage      `json:"estimate"`
	Identifier        string               `json:"identifier"`
	IsDraft           bool                 `json:"is_draft"`
	Labels            []string             `json:"labels"`
	LinkCount         int                  `json:"link_count"`
	Links             []PlaneIssueLink     `json:"links"`
	Modules           []string             `json:"modules"`
	Name              string               `json:"name"`
	Parent            string               `json:"parent"`
	Priority          string               `json:"priority"`
	ProjectIdentifier string               `json:"project_identifier"`
	ProjectName       string               `json:"project_name"`
	Relations         []PlaneIssueRelation `json:"relations"`
	SequenceID        int                  `json:"sequence_id"`
	StartDate         *string              `json:"start_date"`
	StateName         string               `json:"state_name"`
	SubIssuesCount    int                  `json:"sub_issues_count"`
	Subscribers       []string             `json:"subscribers"`
	TargetDate        *string              `json:"target_date"`
	UpdatedAt         string               `json:"updated_at"`
}

// PlaneImportRowError is one row's failure, keyed by Plane `identifier`.
// Rows that fail before an identifier can be read (undecodable payload,
// missing identifier) fall back to the key "row-N" (1-based position in
// the array).
type PlaneImportRowError struct {
	Identifier string `json:"identifier"`
	Message    string `json:"message"`
}

// PlaneImportParse is the outcome of parsing a Plane export: the rows
// that passed shape validation plus the per-row failures. A non-nil
// error return means the WHOLE file failed (not an array, multi-project,
// unreadable zip, ...).
type PlaneImportParse struct {
	ProjectIdentifier string                `json:"project_identifier"`
	ProjectName       string                `json:"project_name"`
	Rows              []PlaneIssueRow       `json:"rows"`
	Errors            []PlaneImportRowError `json:"errors"`
}

// ParsePlaneImport reads a Plane export from r (raw JSON or a zip holding
// a single .json), decodes it row by row, and applies shape validation.
// No writes, no database.
func ParsePlaneImport(r io.Reader) (*PlaneImportParse, error) {
	data, err := readPlaneImportBytes(r)
	if err != nil {
		return nil, err
	}
	rawRows, err := splitPlaneRows(data)
	if err != nil {
		return nil, err
	}
	if len(rawRows) == 0 {
		return nil, ErrPlaneImportEmpty
	}

	out := &PlaneImportParse{Rows: []PlaneIssueRow{}, Errors: []PlaneImportRowError{}}
	for i, raw := range rawRows {
		row, rowErr := decodePlaneRow(raw)
		if rowErr != nil {
			out.Errors = append(out.Errors, PlaneImportRowError{
				Identifier: planeRowKey(raw, i),
				Message:    rowErr.Error(),
			})
			continue
		}
		if err := validatePlaneRow(row); err != nil {
			key := row.Identifier
			if key == "" {
				// Missing identifier: fall back to the row-N key so the
				// error is still attributable.
				key = "row-" + strconv.Itoa(i+1)
			}
			out.Errors = append(out.Errors, PlaneImportRowError{
				Identifier: key,
				Message:    err.Error(),
			})
			continue
		}
		out.Rows = append(out.Rows, *row)
	}

	// One import run = one Plane project -> one glance project.
	projects := map[string]bool{}
	for _, row := range out.Rows {
		projects[row.ProjectIdentifier] = true
	}
	if len(projects) > 1 {
		names := make([]string, 0, len(projects))
		for p := range projects {
			names = append(names, p)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("%w: %s", ErrPlaneImportMultiProject, strings.Join(names, ", "))
	}
	if len(out.Rows) > 0 {
		out.ProjectIdentifier = out.Rows[0].ProjectIdentifier
		out.ProjectName = out.Rows[0].ProjectName
	}
	return out, nil
}

// readPlaneImportBytes reads the whole input (capped), sniffing zip vs
// raw JSON on the first two bytes ("PK" = zip), and strips a UTF-8 BOM.
func readPlaneImportBytes(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, planeImportMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("service: read plane export: %w", err)
	}
	if int64(len(data)) > planeImportMaxBytes {
		return nil, fmt.Errorf("service: plane export exceeds %d MiB size cap", planeImportMaxBytes>>20)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(data) >= 2 && data[0] == 'P' && data[1] == 'K' {
		return readPlaneZipEntry(data)
	}
	return data, nil
}

// readPlaneZipEntry extracts the single .json entry from a Plane download
// zip. Zero or multiple .json entries is an error: we decode the export
// Plane actually produces, and guessing between several files would be a
// silent wrong choice.
func readPlaneZipEntry(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("service: invalid plane export zip: %w", err)
	}
	var names []string
	var entry *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(f.Name), ".json") {
			continue
		}
		names = append(names, f.Name)
		entry = f
	}
	switch len(names) {
	case 0:
		return nil, errors.New("service: plane export zip contains no .json entry")
	case 1:
		// fall through
	default:
		return nil, fmt.Errorf("service: plane export zip must contain a single .json entry, found: %s",
			strings.Join(names, ", "))
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("service: read plane export zip entry: %w", err)
	}
	defer rc.Close()
	payload, err := io.ReadAll(io.LimitReader(rc, planeImportMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("service: read plane export zip entry: %w", err)
	}
	if int64(len(payload)) > planeImportMaxBytes {
		return nil, fmt.Errorf("service: plane export zip entry exceeds %d MiB size cap", planeImportMaxBytes>>20)
	}
	return bytes.TrimPrefix(payload, []byte("\xef\xbb\xbf")), nil
}

// splitPlaneRows checks the top level is a JSON array and splits it into
// per-row payloads. Malformed JSON is a clean error, not a panic.
func splitPlaneRows(data []byte) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, ErrPlaneImportEmpty
	}
	if trimmed[0] != '[' {
		kind := "invalid JSON"
		if trimmed[0] == '{' {
			kind = "a JSON object (real Plane exports are bare arrays)"
		}
		return nil, fmt.Errorf("%w: got %s", ErrPlaneImportShape, kind)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, fmt.Errorf("service: plane export is not valid JSON: %w", err)
	}
	return raw, nil
}

// decodePlaneRow decodes one row payload. Unknown extra fields are
// tolerated; wrong types fail this row only.
func decodePlaneRow(raw json.RawMessage) (*PlaneIssueRow, error) {
	var row PlaneIssueRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, fmt.Errorf("cannot decode issue row: %w", err)
	}
	return &row, nil
}

// planeRowKey recovers the identifier for a row that failed to decode
// (for the per-row error key), falling back to "row-N".
func planeRowKey(raw json.RawMessage, i int) string {
	var probe struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && probe.Identifier != "" {
		return probe.Identifier
	}
	return "row-" + strconv.Itoa(i+1)
}

// validatePlaneRow applies the per-row shape contract: identifier, name,
// project_identifier.
func validatePlaneRow(row *PlaneIssueRow) error {
	if _, _, err := ParsePlaneIdentifier(row.Identifier); err != nil {
		return err
	}
	if strings.TrimSpace(row.Name) == "" {
		return fmt.Errorf("issue %s: name is required", row.Identifier)
	}
	if strings.TrimSpace(row.ProjectIdentifier) == "" {
		return fmt.Errorf("issue %s: project_identifier is required", row.Identifier)
	}
	return nil
}
