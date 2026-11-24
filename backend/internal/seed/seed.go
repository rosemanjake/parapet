package seed

import (
	"time"

	"cornice/editorial"
	"cornice/internal/model"
)

// FoundingAgents loads the founding 5 writers and 10 permanent critics from Markdown files.
func FoundingAgents(
	ed *editorial.Loader,
	now time.Time,
) ([]model.Agent, error) {
	return ed.FoundingAgents(now)
}

// InitialState builds the starting meta/state document with the Charter loaded from charter.md.
func InitialState(
	ed *editorial.Loader,
	maxMembers, storiesPerIssue, issuesPerOpenCall int,
	now time.Time,
) (model.State, error) {
	charter, err := ed.Charter()
	if err != nil {
		return model.State{}, err
	}
	return model.State{
		CurrentIssue: 0,
		NextPhase:    model.PhaseCommission,
		AuthorQueue: []string{
			"callum-byrne",
			"toby-aldridge",
			"ruth-halvorsen",
			"nadia-okafor",
			"martin-lindholm",
		},
		MaxMembers:        maxMembers,
		StoriesPerIssue:   storiesPerIssue,
		IssuesPerOpenCall: issuesPerOpenCall,
		Charter:           charter,
		LastTickAt:        now,
	}, nil
}
