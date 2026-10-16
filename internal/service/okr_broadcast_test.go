package service

// OKR broadcast (C43T1): every objective / key-result mutation
// announces exactly one objective.updated event on the workspace
// channel with the parent objective id as the payload. A nil
// broadcaster stays silent; failed mutations announce nothing.

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// capturingBroadcaster is a fake Broadcaster recording every event
// it receives, for asserting exact type / channel / payload.
type capturingBroadcaster struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	channels  []string
	eventType string
	data      any
}

func (b *capturingBroadcaster) Broadcast(channels []string, eventType string, data any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, capturedEvent{channels: channels, eventType: eventType, data: data})
}

func (b *capturingBroadcaster) snapshot() []capturedEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]capturedEvent, len(b.events))
	copy(out, b.events)
	return out
}

// installCapturingBroadcaster swaps in a fake Broadcaster and
// restores the previous value on test cleanup. Service tests run
// sequentially (no t.Parallel), so mutating the package-global
// Realtime is safe here.
func installCapturingBroadcaster(t *testing.T) *capturingBroadcaster {
	t.Helper()
	prev := Realtime
	fake := &capturingBroadcaster{}
	Realtime = fake
	t.Cleanup(func() { Realtime = prev })
	return fake
}

// assertObjectiveEventAt asserts the event at index i is
// objective.updated on workspace:{slug} with payload {id:
// objectiveID}.
func assertObjectiveEventAt(t *testing.T, fake *capturingBroadcaster, i int, slug, objectiveID string) {
	t.Helper()
	events := fake.snapshot()
	if i >= len(events) {
		t.Fatalf("captured %d events, want at least %d: %+v", len(events), i+1, events)
	}
	ev := events[i]
	if ev.eventType != EventObjectiveUpdated {
		t.Fatalf("event[%d].eventType = %q, want %q", i, ev.eventType, EventObjectiveUpdated)
	}
	if ev.eventType != "objective.updated" {
		t.Fatalf("event[%d].eventType = %q, want %q", i, ev.eventType, "objective.updated")
	}
	if want := []string{"workspace:" + slug}; !reflect.DeepEqual(ev.channels, want) {
		t.Fatalf("event[%d].channels = %v, want %v", i, ev.channels, want)
	}
	payload, ok := ev.data.(map[string]string)
	if !ok {
		t.Fatalf("event[%d].payload type = %T, want map[string]string", i, ev.data)
	}
	if want := map[string]string{"id": objectiveID}; !reflect.DeepEqual(payload, want) {
		t.Fatalf("event[%d].payload = %v, want %v", i, payload, want)
	}
}

// assertSingleObjectiveEvent asserts the fake captured exactly one
// event: objective.updated on workspace:{slug} with payload
// {id: objectiveID}.
func assertSingleObjectiveEvent(t *testing.T, fake *capturingBroadcaster, slug, objectiveID string) {
	t.Helper()
	if n := len(fake.snapshot()); n != 1 {
		t.Fatalf("captured %d events, want exactly 1: %+v", n, fake.snapshot())
	}
	assertObjectiveEventAt(t, fake, 0, slug, objectiveID)
}

func assertZeroEvents(t *testing.T, fake *capturingBroadcaster) {
	t.Helper()
	if events := fake.snapshot(); len(events) != 0 {
		t.Fatalf("captured %d events, want 0: %+v", len(events), events)
	}
}

func TestObjectiveBroadcastCreateObjective(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)
	fake := installCapturingBroadcaster(t)

	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	assertSingleObjectiveEvent(t, fake, slug, ob.ID)
}

func TestObjectiveBroadcastUpdateObjective(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if _, err := UpdateObjective(ctx, pool, slug, actor, ob.ID, ObjectivePatch{Title: strptr("Grow revenue 2x")}); err != nil {
		t.Fatalf("UpdateObjective: %v", err)
	}
	assertSingleObjectiveEvent(t, fake, slug, ob.ID)
}

func TestObjectiveBroadcastDeleteObjective(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := DeleteObjective(ctx, pool, slug, actor, ob.ID); err != nil {
		t.Fatalf("DeleteObjective: %v", err)
	}
	assertSingleObjectiveEvent(t, fake, slug, ob.ID)
}

func TestObjectiveBroadcastCreateKeyResult(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title:       "MRR",
		Kind:        "metric",
		TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	if kr.ObjectiveID != ob.ID {
		t.Fatalf("KR objective = %q, want %q", kr.ObjectiveID, ob.ID)
	}
	// The payload carries the PARENT objective id, not the KR id.
	assertSingleObjectiveEvent(t, fake, slug, ob.ID)
}

func TestObjectiveBroadcastUpdateKeyResult(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title:       "MRR",
		Kind:        "metric",
		TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	// Title-only patch: any KR mutation is an objective change.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{Title: strptr("MRR v2")}); err != nil {
		t.Fatalf("UpdateKeyResult title-only: %v", err)
	}
	assertSingleObjectiveEvent(t, fake, slug, ob.ID)

	// Current-value change (check-in write): rides the SAME call —
	// still exactly one more event, no separate check-in event type.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{CurrentValue: fptr(42)}); err != nil {
		t.Fatalf("UpdateKeyResult value change: %v", err)
	}
	if n := len(fake.snapshot()); n != 2 {
		t.Fatalf("captured %d events after two KR updates, want 2: %+v", n, fake.snapshot())
	}
	assertObjectiveEventAt(t, fake, 1, slug, ob.ID)
}

func TestObjectiveBroadcastDeleteKeyResult(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title:       "MRR",
		Kind:        "metric",
		TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := DeleteKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID); err != nil {
		t.Fatalf("DeleteKeyResult: %v", err)
	}
	assertSingleObjectiveEvent(t, fake, slug, ob.ID)
}

func TestObjectiveBroadcastNilBroadcasterSilent(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)
	prev := Realtime
	Realtime = nil
	t.Cleanup(func() { Realtime = prev })

	// No panic, no error: announce is a silent no-op with no
	// broadcaster wired.
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	if _, err := UpdateObjective(ctx, pool, slug, actor, ob.ID, ObjectivePatch{Title: strptr("Grow revenue 2x")}); err != nil {
		t.Fatalf("UpdateObjective: %v", err)
	}
	if err := DeleteObjective(ctx, pool, slug, actor, ob.ID); err != nil {
		t.Fatalf("DeleteObjective: %v", err)
	}
}

func TestObjectiveBroadcastNoAnnounceOnFailedMutation(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)
	fake := installCapturingBroadcaster(t)

	// Failed creates: validation errors, before any mutation.
	if _, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "  "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty title err = %v, want ErrNameRequired", err)
	}
	if _, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "X", Status: "bogus"}); !errors.Is(err, ErrInvalidObjective) {
		t.Fatalf("bad status err = %v, want ErrInvalidObjective", err)
	}
	assertZeroEvents(t, fake)

	// One successful objective create announces exactly once.
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Grow revenue"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	assertSingleObjectiveEvent(t, fake, slug, ob.ID)

	// Failed objective update / delete: no further events.
	if _, err := UpdateObjective(ctx, pool, slug, actor, ob.ID, ObjectivePatch{Status: strptr("bogus")}); !errors.Is(err, ErrInvalidObjective) {
		t.Fatalf("bad status update err = %v, want ErrInvalidObjective", err)
	}
	if _, err := UpdateObjective(ctx, pool, slug, actor, ob.ID, ObjectivePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch err = %v, want ErrNothingToUpdate", err)
	}
	if err := DeleteObjective(ctx, pool, slug, actor, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrObjectiveNotFound) {
		t.Fatalf("delete unknown err = %v, want ErrObjectiveNotFound", err)
	}
	if n := len(fake.snapshot()); n != 1 {
		t.Fatalf("captured %d events after failed objective mutations, want 1: %+v", n, fake.snapshot())
	}

	// Failed KR create: zero-span target rejected before any mutation.
	if _, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title:       "Zero span",
		Kind:        "metric",
		StartValue:  fptr(50),
		TargetValue: fptr(50),
	}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("zero-span err = %v, want ErrInvalidKeyResult", err)
	}
	if n := len(fake.snapshot()); n != 1 {
		t.Fatalf("captured %d events after failed KR create, want 1: %+v", n, fake.snapshot())
	}

	// One successful KR create announces exactly one more event.
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title:       "MRR",
		Kind:        "metric",
		TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	if n := len(fake.snapshot()); n != 2 {
		t.Fatalf("captured %d events after successful KR create, want 2: %+v", n, fake.snapshot())
	}
	assertObjectiveEventAt(t, fake, 1, slug, ob.ID)

	// Failed KR update / delete: no further events.
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{Kind: strptr("issue")}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("kind change err = %v, want ErrInvalidKeyResult", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, KeyResultPatch{TargetValue: fptr(0)}); !errors.Is(err, ErrInvalidKeyResult) {
		t.Fatalf("zero-span update err = %v, want ErrInvalidKeyResult", err)
	}
	if err := DeleteKeyResult(ctx, pool, slug, actor, ob.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrKeyResultNotFound) {
		t.Fatalf("delete unknown KR err = %v, want ErrKeyResultNotFound", err)
	}
	if n := len(fake.snapshot()); n != 2 {
		t.Fatalf("captured %d events after failed KR mutations, want 2: %+v", n, fake.snapshot())
	}
}
