package node

import (
	"bytes"
	"testing"
)

func TestControllerMatchesNodeV1(t *testing.T) {
	db, client, err := openEmptyNodeV1Database(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	controller := bytes.Repeat([]byte{7}, 32)
	if matched, err := controllerMatchesNodeV1(t.Context(), client, controller); err != nil || matched {
		t.Fatalf("unclaimed match = %t, %v", matched, err)
	}
	if _, err := client.ControllerBinding.Create().SetID(1).SetControllerPublic(controller).Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	if matched, err := controllerMatchesNodeV1(t.Context(), client, controller); err != nil || !matched {
		t.Fatalf("matching Controller = %t, %v", matched, err)
	}
	if matched, err := controllerMatchesNodeV1(t.Context(), client, bytes.Repeat([]byte{8}, 32)); err != nil || matched {
		t.Fatalf("other Controller = %t, %v", matched, err)
	}
}

func TestClaimNodeV1PreservesFirstController(t *testing.T) {
	db, client, err := openEmptyNodeV1Database(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := bytes.Repeat([]byte{7}, 32)
	second := bytes.Repeat([]byte{8}, 32)
	if claimed, err := claimNodeV1(t.Context(), client, first[:31]); err == nil || claimed {
		t.Fatalf("invalid key claim = %t, %v", claimed, err)
	}
	if claimed, err := claimNodeV1(t.Context(), client, first); err != nil || !claimed {
		t.Fatalf("first claim = %t, %v", claimed, err)
	}
	for _, controller := range [][]byte{first, second} {
		if claimed, err := claimNodeV1(t.Context(), client, controller); err != nil || claimed {
			t.Fatalf("repeated claim = %t, %v", claimed, err)
		}
	}
	if matched, err := controllerMatchesNodeV1(t.Context(), client, first); err != nil || !matched {
		t.Fatalf("first Controller was replaced: %t, %v", matched, err)
	}
}
