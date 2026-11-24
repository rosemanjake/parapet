package editorial

import (
	"testing"
	"time"

	"cornice/internal/model"
)

func TestEmbeddedEditorialLoadsCleanly(t *testing.T) {
	ed := NewLoader("")

	charter, err := ed.Charter()
	if err != nil || len(charter) < 200 {
		t.Fatalf("Charter() err=%v len=%d", err, len(charter))
	}

	agents, err := ed.FoundingAgents(time.Now().UTC())
	if err != nil {
		t.Fatalf("FoundingAgents() err=%v", err)
	}
	writers, critics := 0, 0
	for _, a := range agents {
		if a.Bio == "" || a.VoiceCraft == "" || a.NegativeRules == "" {
			t.Errorf("agent %s missing required markdown sections", a.ID)
		}
		if a.Role == model.RoleWriter {
			writers++
		} else if a.Role == model.RoleCritic {
			critics++
		}
	}
	if writers != 5 || critics != 10 {
		t.Fatalf(
			"expected 5 writers and 10 critics, got %d writers and %d critics",
			writers,
			critics,
		)
	}

	skills := []string{
		"commission-story", "draft-story", "choose-editor", "workshop-edit",
		"revise-story", "revise-metadata", "editorial-package",
		"salon-comment", "review-issue", "reflect-notebook",
		"generate-applicant", "returning-alumnus", "sample-story",
		"vote-open-call", "vote-retention",
	}
	for _, s := range skills {
		body, err := ed.Skill(s)
		if err != nil || len(body) < 50 {
			t.Errorf("Skill(%q) err=%v len=%d", s, err, len(body))
		}
	}
}
