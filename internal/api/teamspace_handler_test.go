package api

// C89T0: the PATCH project teamspace_id tri-state must survive JSON
// decoding — absent = unchanged, explicit null = ungroup, UUID = move.
// encoding/json folds a JSON null into a nil *string, so the body uses
// service.PatchField[string]; these tests pin that contract at the
// decode boundary (the service tests cover the storage semantics).

import (
	"encoding/json"
	"testing"
)

func TestUpdateProjectBodyTeamspaceIDTriState(t *testing.T) {
	teamspaceID := "11111111-1111-1111-1111-111111111111"

	// Absent → Set=false (UpdateProject leaves the assignment alone).
	var absent updateProjectBody
	if err := json.Unmarshal([]byte(`{"name":"P One"}`), &absent); err != nil {
		t.Fatalf("unmarshal absent: %v", err)
	}
	if absent.TeamspaceID.Set {
		t.Fatalf("absent teamspace_id: Set = true, want false")
	}

	// Explicit null → Set=true, Value=nil (ungroup).
	var nulled updateProjectBody
	if err := json.Unmarshal([]byte(`{"teamspace_id":null}`), &nulled); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if !nulled.TeamspaceID.Set || nulled.TeamspaceID.Value != nil {
		t.Fatalf("null teamspace_id: Set=%v Value=%v, want Set=true Value=nil",
			nulled.TeamspaceID.Set, nulled.TeamspaceID.Value)
	}

	// UUID → Set=true, Value=uuid (move).
	var moved updateProjectBody
	if err := json.Unmarshal([]byte(`{"teamspace_id":"`+teamspaceID+`"}`), &moved); err != nil {
		t.Fatalf("unmarshal uuid: %v", err)
	}
	if !moved.TeamspaceID.Set || moved.TeamspaceID.Value == nil || *moved.TeamspaceID.Value != teamspaceID {
		t.Fatalf("uuid teamspace_id: Set=%v Value=%v, want Set=true Value=%s",
			moved.TeamspaceID.Set, moved.TeamspaceID.Value, teamspaceID)
	}

	// Wrong JSON type → decode error (400 at the handler), never a
	// silent ignore.
	var wrong updateProjectBody
	if err := json.Unmarshal([]byte(`{"teamspace_id":42}`), &wrong); err == nil {
		t.Fatalf("numeric teamspace_id decoded without error")
	}
}
