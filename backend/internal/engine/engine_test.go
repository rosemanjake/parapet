package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"cornice/internal/model"
	"cornice/internal/store"
)

func TestUniqueStoryIDPreventsCollisions(t *testing.T) {
	id1 := uniqueStoryID(1, "The Fence", "callum-byrne")
	id2 := uniqueStoryID(1, "The Fence", "ruth-halvorsen")
	if id1 == id2 {
		t.Fatalf(
			"expected distinct IDs for identical titles by different authors, got %q",
			id1,
		)
	}
	if id1 != "issue-0001-the-fence-byrne" {
		t.Fatalf("unexpected format: %q", id1)
	}
}

func TestApplyRelationshipDeltaClampsPerTurn(t *testing.T) {
	a := model.Agent{ID: "callum-byrne"}
	applyRelationshipDelta(&a, "ruth-halvorsen", 10, -10, "Sharp edit.")
	rel := a.Relationships["ruth-halvorsen"]
	if rel.Trust != 2 || rel.Respect != -2 {
		t.Fatalf(
			"expected per-turn clamp to (+2, -2), got (%d, %d)",
			rel.Trust,
			rel.Respect,
		)
	}
	// Self-updates must be ignored.
	applyRelationshipDelta(&a, "callum-byrne", 2, 2, "Self")
	if _, ok := a.Relationships["callum-byrne"]; ok {
		t.Fatalf("expected self relationship update to be ignored")
	}
}

func TestUpsertStandingIsIdempotent(t *testing.T) {
	var a model.Agent
	e1 := model.StandingEntry{
		IssueNumber: 1,
		StoryID:     "s1",
		StoryTitle:  "One",
		Verdicts:    []string{"Mixed"},
	}
	upsertStanding(&a, e1)
	upsertStanding(&a, e1)
	if len(a.Standing) != 1 {
		t.Fatalf(
			"expected 1 standing entry after duplicate upsert, got %d",
			len(a.Standing),
		)
	}
}

func TestPickEjectionTargetBreaksTiesByLowestStanding(t *testing.T) {
	counts := map[string]int{"callum-byrne": 2, "martin-lindholm": 2}
	members := map[string]model.Agent{
		"callum-byrne": {
			ID: "callum-byrne",
			Standing: []model.StandingEntry{
				{Verdicts: []string{"Acclaimed", "Acclaimed"}},
			},
		},
		"martin-lindholm": {
			ID: "martin-lindholm",
			Standing: []model.StandingEntry{
				{Verdicts: []string{"Panned", "Mixed"}},
			},
		},
	}
	for i := 0; i < 10; i++ {
		if got := pickEjectionTarget(counts, members); got != "martin-lindholm" {
			t.Fatalf(
				"expected lowest-standing tied member martin-lindholm, got %q",
				got,
			)
		}
	}
}

func TestLocalStoreLocking(t *testing.T) {
	st, err := store.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.SaveState(ctx, model.State{CurrentIssue: 1, NextPhase: model.PhaseCommission}); err != nil {
		t.Fatal(err)
	}
	if err := st.AcquireLock(ctx, "owner-a", time.Minute); err != nil {
		t.Fatalf("AcquireLock owner-a failed: %v", err)
	}
	if err := st.AcquireLock(ctx, "owner-b", time.Minute); !errors.Is(
		err,
		store.ErrLocked,
	) {
		t.Fatalf("expected ErrLocked for owner-b, got %v", err)
	}
	if err := st.ReleaseLock(ctx, "owner-a"); err != nil {
		t.Fatalf("ReleaseLock failed: %v", err)
	}
	if err := st.AcquireLock(ctx, "owner-b", time.Minute); err != nil {
		t.Fatalf("AcquireLock owner-b after release failed: %v", err)
	}
}
