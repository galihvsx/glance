package service

// KR check-in history (C40T0): seed entry on metric-KR create,
// entry on value change (previous/new/note/author), nothing on
// no-op / title / target-only updates, nothing for issue KRs, note
// validation (1000-char cap, empty → NULL), list ordering newest
// first, cap at 100, author resolution (member, null author), and
// role gates.

import (
	"errors"
	"strings"
	"testing"
)

func TestKRCheckinHistory(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)
	actorEmail := ""
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1::uuid`, actor).Scan(&actorEmail); err != nil {
		t.Fatalf("actor email: %v", err)
	}

	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Check-ins"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}

	// Metric KR with an explicit current value → seed entry:
	// previous NULL, new = inserted current, note NULL, author = actor.
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "NPS", Kind: "metric", Unit: strptr("%"),
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(25),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	ins, err := ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (seed): %v", err)
	}
	if len(ins) != 1 {
		t.Fatalf("check-ins after create = %d, want 1", len(ins))
	}
	seed := ins[0]
	if seed.PreviousValue != nil {
		t.Fatalf("seed previous = %v, want nil", *seed.PreviousValue)
	}
	if seed.NewValue != 25 || seed.Note != nil {
		t.Fatalf("seed = new %v note %v, want new 25 note nil", seed.NewValue, seed.Note)
	}
	if seed.KeyResultID != kr.ID || seed.ID == "" {
		t.Fatalf("seed ids = %+v", seed)
	}
	if seed.Author == nil || seed.Author.ID != actor {
		t.Fatalf("seed author = %+v, want id %s", seed.Author, actor)
	}
	// createTestUser inserts email only (name NULL) → display name
	// falls back to email (COALESCE rule, same as workload).
	if seed.Author.DisplayName != actorEmail {
		t.Fatalf("seed display_name = %q, want %q", seed.Author.DisplayName, actorEmail)
	}

	// Metric KR with no CurrentValue → current defaults to 0, and
	// the seed records the inserted current (0), not start_value.
	kr0, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Zero", Kind: "metric",
		StartValue: fptr(10), TargetValue: fptr(20),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult default current: %v", err)
	}
	ins0, err := ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr0.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (default): %v", err)
	}
	if len(ins0) != 1 || ins0[0].NewValue != 0 || ins0[0].PreviousValue != nil {
		t.Fatalf("default seed = %+v, want 1 entry new 0 previous nil", ins0)
	}

	// Value change with a padded note → entry, note trimmed.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(50), CheckinNote: strptr("  Q1 push  "),
	}); err != nil {
		t.Fatalf("UpdateKeyResult value+note: %v", err)
	}
	ins, err = ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (after change): %v", err)
	}
	if len(ins) != 2 {
		t.Fatalf("check-ins after change = %d, want 2", len(ins))
	}
	// Newest first: the change entry, then the seed.
	latest := ins[0]
	if latest.PreviousValue == nil || *latest.PreviousValue != 25 || latest.NewValue != 50 {
		t.Fatalf("latest = prev %v new %v, want prev 25 new 50", latest.PreviousValue, latest.NewValue)
	}
	if latest.Note == nil || *latest.Note != "Q1 push" {
		t.Fatalf("latest note = %v, want %q", latest.Note, "Q1 push")
	}
	if latest.Author == nil || latest.Author.ID != actor {
		t.Fatalf("latest author = %+v", latest.Author)
	}
	if ins[1].PreviousValue != nil || ins[1].NewValue != 25 {
		t.Fatalf("seed moved: %+v", ins[1])
	}

	// No-op update (same value, with a note) records nothing.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(50), CheckinNote: strptr("no-op"),
	}); err != nil {
		t.Fatalf("UpdateKeyResult no-op: %v", err)
	}
	ins, err = ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (no-op): %v", err)
	}
	if len(ins) != 2 {
		t.Fatalf("check-ins after no-op = %d, want 2", len(ins))
	}

	// Title-only change (with a note) records nothing.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		Title: strptr("NPS v2"), CheckinNote: strptr("title only"),
	}); err != nil {
		t.Fatalf("UpdateKeyResult title-only: %v", err)
	}
	// Target-only change (with a note) records nothing.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		TargetValue: fptr(200), CheckinNote: strptr("target only"),
	}); err != nil {
		t.Fatalf("UpdateKeyResult target-only: %v", err)
	}
	ins, err = ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (title/target): %v", err)
	}
	if len(ins) != 2 {
		t.Fatalf("check-ins after title/target-only = %d, want 2", len(ins))
	}

	// Value change with an empty / whitespace note → entry, note NULL.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(60), CheckinNote: strptr("   "),
	}); err != nil {
		t.Fatalf("UpdateKeyResult empty note: %v", err)
	}
	ins, err = ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (empty note): %v", err)
	}
	if len(ins) != 3 || ins[0].Note != nil || ins[0].NewValue != 60 {
		t.Fatalf("empty-note entry = %+v, want 3 entries, newest new 60 note nil", ins)
	}

	// Note over 1000 chars → 400-equivalent, and no entry recorded.
	long := strings.Repeat("a", 1001)
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(70), CheckinNote: &long,
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("1001-char note err = %v, want ErrInvalidKeyResult", err)
	}
	// The cap counts characters (runes), not bytes: 1001 multibyte
	// runes fail, 1000 pass.
	longRunes := strings.Repeat("é", 1001)
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(70), CheckinNote: &longRunes,
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("1001-rune note err = %v, want ErrInvalidKeyResult", err)
	}
	okRunes := strings.Repeat("é", 1000)
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(70), CheckinNote: &okRunes,
	}); err != nil {
		t.Fatalf("1000-rune note: %v", err)
	}
	ins, err = ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (note cap): %v", err)
	}
	if len(ins) != 4 || ins[0].NewValue != 70 || ins[0].Note == nil || len([]rune(*ins[0].Note)) != 1000 {
		t.Fatalf("after note-cap checks: %d entries, newest = %+v", len(ins), ins[0])
	}

	// A checkin_note-only patch changes nothing → nothing to update.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{
		CheckinNote: strptr("orphan note"),
	}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("note-only patch err = %v, want ErrNothingToUpdate", err)
	}

	// A second member's update attributes the entry to them.
	member := createTestUser(t, pool, uniqueTestEmail("okr-member"))
	if err := UpsertMember(ctx, pool, slug, actor, member, RoleMember); err != nil {
		t.Fatalf("UpsertMember member: %v", err)
	}
	memberEmail := ""
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1::uuid`, member).Scan(&memberEmail); err != nil {
		t.Fatalf("member email: %v", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, member, ob.ID, kr.ID, KeyResultPatch{
		CurrentValue: fptr(80),
	}); err != nil {
		t.Fatalf("UpdateKeyResult by member: %v", err)
	}
	ins, err = ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (member): %v", err)
	}
	if len(ins) != 5 || ins[0].Author == nil || ins[0].Author.ID != member || ins[0].Author.DisplayName != memberEmail {
		t.Fatalf("member entry = %+v, want author %s / %s", ins[0], member, memberEmail)
	}

	// Issue KRs: no seed, list is [], and (per existing rules)
	// current_value patches stay rejected.
	ikr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}
	iins, err := ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, ikr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (issue KR): %v", err)
	}
	if len(iins) != 0 {
		t.Fatalf("issue KR check-ins = %d, want 0", len(iins))
	}

	// Error paths: unknown KR → 404-equivalent, malformed → 400,
	// unknown objective → 404, non-member → ErrNotFound, guest → 403.
	if _, err := ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrKeyResultNotFound) {
		t.Fatalf("unknown KR err = %v, want ErrKeyResultNotFound", err)
	}
	if _, err := ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, "not-a-uuid"); !errors.Is(err, ErrInvalidObjectiveID) {
		t.Fatalf("malformed KR err = %v, want ErrInvalidObjectiveID", err)
	}
	if _, err := ListKeyResultCheckins(ctx, pool, slug, actor, "00000000-0000-0000-0000-000000000000", kr.ID); !errors.Is(err, ErrObjectiveNotFound) {
		t.Fatalf("unknown objective err = %v, want ErrObjectiveNotFound", err)
	}
	outsider := createTestUser(t, pool, uniqueTestEmail("okr-outsider"))
	if _, err := ListKeyResultCheckins(ctx, pool, slug, outsider, ob.ID, kr.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member err = %v, want ErrNotFound", err)
	}
	guest := setupOKRGuest(t, pool, slug, actor)
	if _, err := ListKeyResultCheckins(ctx, pool, slug, guest, ob.ID, kr.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest list err = %v, want ErrForbidden", err)
	}
}

func TestKRCheckinAuthorNullAndCap(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)

	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Cap"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Cap KR", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(1000),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}

	// A row with a NULL author reads back with author nil (the
	// ON DELETE SET NULL shape — e.g. the user was deleted).
	if _, err := pool.Exec(ctx,
		`INSERT INTO key_result_checkins (key_result_id, author_id, previous_value, new_value, note, created_at)
		 VALUES ($1::uuid, NULL, 0, 1, 'orphaned', now() + interval '1 hour')`,
		kr.ID); err != nil {
		t.Fatalf("insert null-author row: %v", err)
	}
	ins, err := ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins: %v", err)
	}
	if len(ins) != 2 || ins[0].Author != nil || ins[0].Note == nil || *ins[0].Note != "orphaned" {
		t.Fatalf("null-author entry = %+v, want newest with author nil", ins)
	}

	// Cap: 105 further rows with ascending timestamps → the list
	// returns exactly the 100 newest, newest first.
	for i := 1; i <= 105; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO key_result_checkins (key_result_id, author_id, previous_value, new_value, created_at)
			 VALUES ($1::uuid, $2::uuid, $3, $4, now() + make_interval(secs => $5))`,
			kr.ID, actor, float64(i-1), float64(i), 3600+i); err != nil {
			t.Fatalf("insert cap row %d: %v", i, err)
		}
	}
	ins, err = ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (cap): %v", err)
	}
	if len(ins) != 100 {
		t.Fatalf("capped list = %d, want 100", len(ins))
	}
	if ins[0].NewValue != 105 || ins[99].NewValue != 6 {
		t.Fatalf("capped range = %v..%v, want 105..6", ins[0].NewValue, ins[99].NewValue)
	}
	for i := 1; i < len(ins); i++ {
		if !ins[i-1].CreatedAt.After(ins[i].CreatedAt) && !ins[i-1].CreatedAt.Equal(ins[i].CreatedAt) {
			t.Fatalf("not newest-first at %d", i)
		}
	}
}
