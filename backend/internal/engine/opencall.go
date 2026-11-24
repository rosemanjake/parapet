package engine

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/adk/v2/agent/llmagent"

	"cornice/internal/adkrun"
	"cornice/internal/model"
	"cornice/internal/prompts"
)

type applicantProfileArgs struct {
	Name                 string `json:"name"                  jsonschema:"Grounded, ordinary-sounding real-world writer name"`
	Genre                string `json:"genre"                 jsonschema:"Distinct genre or literary niche not duplicated by an active member"`
	Bio                  string `json:"bio"                   jsonschema:"Concise, credible writer biography"`
	VoiceCraft           string `json:"voice_craft"           jsonschema:"Distinct sentence rhythm, structural habits, and craft directives"`
	NegativeRules        string `json:"negative_rules"        jsonschema:"What this writer despises and refuses to write"`
	EditorialTemperament string `json:"editorial_temperament" jsonschema:"How this writer behaves as a workshop editor and voter"`
	CoverLetter          string `json:"cover_letter"          jsonschema:"150-240 word cover letter addressed to the Cornice collective"`
	SampleTitle          string `json:"sample_title"          jsonschema:"Crisp literary title for the sample story"`
	SamplePremise        string `json:"sample_premise"        jsonschema:"2-sentence premise of the sample story to be drafted next"`
}

type returningPitchArgs struct {
	CoverLetter   string `json:"cover_letter"   jsonschema:"150-240 word re-application cover letter addressed to the collective"`
	SampleTitle   string `json:"sample_title"   jsonschema:"Crisp literary title for the new sample piece"`
	SamplePremise string `json:"sample_premise" jsonschema:"2-sentence premise of the new sample story"`
}

type openCallBallotArgs struct {
	AdmitID     string `json:"admit_id"               jsonschema:"Exact Applicant ID to admit, or 'none' if no applicant clears Cornice's bar"`
	AdmitReason string `json:"admit_reason"           jsonschema:"2-3 candid sentences explaining your admission vote"`
	EjectID     string `json:"eject_id,omitempty"     jsonschema:"If at capacity and admit_id != 'none', exact ID of one current member (not yourself) to eject; otherwise empty"`
	EjectReason string `json:"eject_reason,omitempty" jsonschema:"If eject_id is set, 2-3 candid sentences explaining why they should lose their seat"`
}

type retentionBallotArgs struct {
	Vote   string `json:"vote"   jsonschema:"Strictly 'retain' or 'eject'"`
	Reason string `json:"reason" jsonschema:"2-3 candid sentences explaining your vote"`
}

// runOpenCall executes:
// 1. Standing-triggered Retention Votes FIRST (frees seats naturally before capacity checks, prevents newcomers voting on veterans).
// 2. Applicant generation (2 fresh + 1 fairly rotated returning alumnus).
// 3. Parallel member ballots (using 1 shared ArchiveSnapshot).
// 4. Atomic-style commit of admissions/ejections & OpenCall record.
func (e *Engine) runOpenCall(
	ctx context.Context,
	state *model.State,
	maxMembers int,
	advancePhase bool,
) error {
	genSkill, err := e.mustSkill("generate-applicant")
	if err != nil {
		return err
	}
	retSkill, err := e.mustSkill("returning-alumnus")
	if err != nil {
		return err
	}
	sampleSkill, err := e.mustSkill("sample-story")
	if err != nil {
		return err
	}
	voteSkill, err := e.mustSkill("vote-open-call")
	if err != nil {
		return err
	}

	// Single shared ArchiveSnapshot for the entire Open Call phase.
	archiveSnap, err := adkrun.LoadArchiveSnapshot(ctx, e.store)
	if err != nil {
		return err
	}

	// Step 1: Run standing-triggered Retention Votes FIRST before admissions or capacity checks.
	motions, retentionEjected, err := e.runRetentionVotes(
		ctx,
		state,
		archiveSnap,
	)
	if err != nil {
		return err
	}

	// Reload agents after retention outcomes so the active roster and alumni pool are current.
	allAgents, err := e.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	activeWriters := filterAgents(
		allAgents,
		model.RoleWriter,
		model.StatusActive,
	)
	alumni := filterAgents(allAgents, model.RoleWriter, model.StatusEjected)
	existingNames := map[string]bool{}
	for _, a := range allAgents {
		existingNames[strings.ToLower(a.ID)] = true
	}

	recentSummary := e.recentIssuesDigest(ctx, 2)

	// Step 2a: Generate 2 fresh applicant profiles sequentially (so slot 2 avoids slot 1's genre),
	// then generate their sample stories in parallel.
	var applicants []model.Applicant
	var samplePremises []string
	for slot := 1; slot <= 2; slot++ {
		log.Printf("[open_call] generating fresh applicant #%d...", slot)
		avoid := append([]model.Agent(nil), activeWriters...)
		for _, prev := range applicants {
			avoid = append(
				avoid,
				model.Agent{Name: prev.Name, Genre: prev.Genre, Bio: prev.Bio},
			)
		}
		gen, err := adkrun.RunAction(ctx, e.adk, adkrun.Request{
			AgentName:   fmt.Sprintf("applicant_gen_%d", slot),
			Instruction: prompts.CharterInstruction(state.Charter, genSkill),
			UserMessage: prompts.GenerateApplicantInput(
				avoid,
				recentSummary,
				slot,
			),
			Temperature: 0.95,
		}, "submit_applicant_profile", "Submit a complete new applicant persona, cover letter, and sample pitch.",
			func(a applicantProfileArgs) error {
				if strings.TrimSpace(a.Name) == "" ||
					strings.TrimSpace(a.Genre) == "" ||
					strings.TrimSpace(a.Bio) == "" ||
					strings.TrimSpace(a.VoiceCraft) == "" ||
					strings.TrimSpace(a.NegativeRules) == "" ||
					strings.TrimSpace(a.EditorialTemperament) == "" ||
					strings.TrimSpace(a.CoverLetter) == "" ||
					strings.TrimSpace(a.SampleTitle) == "" ||
					strings.TrimSpace(a.SamplePremise) == "" {
					return fmt.Errorf(
						"all applicant profile fields must be non-empty",
					)
				}
				return nil
			})
		if err != nil {
			return fmt.Errorf("generate applicant %d: %w", slot, err)
		}
		id := slugify(gen.Name)
		if existingNames[id] {
			id = fmt.Sprintf("%s-%d", id, state.CurrentIssue)
		}
		existingNames[id] = true
		applicants = append(applicants, model.Applicant{
			ID:                   id,
			Name:                 strings.TrimSpace(gen.Name),
			Genre:                strings.TrimSpace(gen.Genre),
			Bio:                  strings.TrimSpace(gen.Bio),
			VoiceCraft:           strings.TrimSpace(gen.VoiceCraft),
			NegativeRules:        strings.TrimSpace(gen.NegativeRules),
			EditorialTemperament: strings.TrimSpace(gen.EditorialTemperament),
			CoverLetter:          strings.TrimSpace(gen.CoverLetter),
			SampleTitle:          strings.TrimSpace(gen.SampleTitle),
		})
		samplePremises = append(
			samplePremises,
			strings.TrimSpace(gen.SamplePremise),
		)
	}

	// Step 2b: Fairly rotate returning alumni (exclude anyone just ejected in this very Open Call's retention vote).
	justEjected := map[string]bool{}
	for _, id := range retentionEjected {
		justEjected[id] = true
	}
	var eligibleAlumni []model.Agent
	for _, al := range alumni {
		if !justEjected[al.ID] {
			eligibleAlumni = append(eligibleAlumni, al)
		}
	}
	var returningAlumnus *model.Agent
	if len(eligibleAlumni) > 0 {
		sort.Slice(eligibleAlumni, func(i, j int) bool {
			if eligibleAlumni[i].LastAppliedIssue != eligibleAlumni[j].LastAppliedIssue {
				return eligibleAlumni[i].LastAppliedIssue < eligibleAlumni[j].LastAppliedIssue
			}
			return eligibleAlumni[i].EjectedIssue < eligibleAlumni[j].EjectedIssue
		})
		ret := eligibleAlumni[0]
		returningAlumnus = &ret
		log.Printf(
			"[open_call] returning alumnus %s pitching to rejoin...",
			ret.Name,
		)
		pitch, err := adkrun.RunAction(ctx, e.adk, adkrun.Request{
			AgentName:   ret.ID + "_reapply",
			Instruction: prompts.AgentInstruction(state.Charter, ret, retSkill),
			UserMessage: prompts.ReturningAlumnusInput(
				ret,
				activeWriters,
				recentSummary,
			),
			Temperature: 0.9,
		}, "submit_reapplication", "Submit your returning alumnus cover letter and sample story pitch.",
			func(a returningPitchArgs) error {
				if strings.TrimSpace(a.CoverLetter) == "" ||
					strings.TrimSpace(a.SampleTitle) == "" ||
					strings.TrimSpace(a.SamplePremise) == "" {
					return fmt.Errorf(
						"cover_letter, sample_title, and sample_premise must be non-empty",
					)
				}
				return nil
			})
		if err != nil {
			return fmt.Errorf("returning alumnus pitch %s: %w", ret.Name, err)
		}
		applicants = append(applicants, model.Applicant{
			ID:                   ret.ID,
			Name:                 ret.Name,
			Genre:                ret.Genre,
			Bio:                  ret.Bio,
			VoiceCraft:           ret.VoiceCraft,
			NegativeRules:        ret.NegativeRules,
			EditorialTemperament: ret.EditorialTemperament,
			CoverLetter:          strings.TrimSpace(pitch.CoverLetter),
			SampleTitle:          strings.TrimSpace(pitch.SampleTitle),
			IsReturningAlumni:    true,
		})
		samplePremises = append(
			samplePremises,
			strings.TrimSpace(pitch.SamplePremise),
		)
	}

	// Step 2c: Draft all applicant sample stories in parallel.
	gSamples, gsCtx := errgroup.WithContext(ctx)
	gSamples.SetLimit(3)
	for i := range applicants {
		idx := i
		app := applicants[idx]
		premise := samplePremises[idx]
		gSamples.Go(func() error {
			appAgent := model.Agent{
				ID: app.ID, Name: app.Name, Role: model.RoleWriter, Genre: app.Genre,
				Bio: app.Bio, VoiceCraft: app.VoiceCraft, NegativeRules: app.NegativeRules,
				EditorialTemperament: app.EditorialTemperament,
			}
			story, err := e.adk.RunText(gsCtx, adkrun.Request{
				AgentName: app.ID + "_sample",
				Instruction: prompts.AgentInstruction(
					state.Charter,
					appAgent,
					sampleSkill,
				),
				UserMessage: prompts.SampleStoryInput(app.SampleTitle, premise),
				Temperature: 0.9,
			})
			if err != nil {
				return fmt.Errorf("sample story for %s: %w", app.Name, err)
			}
			applicants[idx].SampleStory = story
			log.Printf(
				"  -> applicant: %s (%s) — \"%s\" (%d words)",
				app.Name,
				app.Genre,
				app.SampleTitle,
				len(strings.Fields(story)),
			)
			return nil
		})
	}
	if err := gSamples.Wait(); err != nil {
		return err
	}

	// Step 3: Collect all member ballots in parallel using the shared ArchiveSnapshot.
	appMap := make(map[string]model.Applicant, len(applicants))
	for _, a := range applicants {
		appMap[a.ID] = a
	}
	wMap := agentMap(activeWriters)
	atCap := len(activeWriters) >= maxMembers

	ballots := make([]model.Ballot, len(activeWriters))
	gVotes, gvCtx := errgroup.WithContext(ctx)
	gVotes.SetLimit(2)
	for i, w := range activeWriters {
		idx, voter := i, w
		gVotes.Go(func() error {
			log.Printf("[open_call] %s casting ballot...", voter.Name)
			archive, err := archiveSnap.NewSession(
				voter.Name,
				e.cfg.MaxArchiveReads,
			)
			if err != nil {
				return err
			}
			v, err := adkrun.RunAction(gvCtx, e.adk, adkrun.Request{
				AgentName: voter.ID + "_vote",
				Instruction: prompts.AgentInstruction(
					state.Charter,
					voter,
					voteSkill,
				),
				UserMessage: prompts.OpenCallVoteInput(
					voter,
					activeWriters,
					applicants,
					maxMembers,
					archive.Register,
				),
				Temperature: 0.5,
				Tools:       archive.Tools,
				BeforeToolCallbacks: []llmagent.BeforeToolCallback{
					archive.Guard,
				},
				OnAttemptReset: archive.Reset,
			}, "cast_open_call_ballot", "Cast your Open Call admission (and if at capacity, ejection) ballot.",
				func(a openCallBallotArgs) error {
					admit := strings.TrimSpace(a.AdmitID)
					if admit != "none" {
						if _, ok := appMap[admit]; !ok {
							return fmt.Errorf(
								"admit_id %q is neither 'none' nor a valid applicant ID",
								admit,
							)
						}
					}
					if atCap && admit != "none" {
						eject := strings.TrimSpace(a.EjectID)
						if eject == "" || eject == voter.ID {
							return fmt.Errorf(
								"collective is at capacity; you must specify a valid eject_id (not yourself)",
							)
						}
						if _, ok := wMap[eject]; !ok {
							return fmt.Errorf(
								"eject_id %q is not an active collective member",
								eject,
							)
						}
					}
					return nil
				})
			if err != nil {
				return fmt.Errorf("ballot by %s: %w", voter.Name, err)
			}

			admitID := strings.TrimSpace(v.AdmitID)
			ejectID := strings.TrimSpace(v.EjectID)
			if !atCap || admitID == "none" {
				ejectID = ""
			}
			ballots[idx] = model.Ballot{
				VoterID:     voter.ID,
				VoterName:   voter.Name,
				AdmitID:     admitID,
				AdmitReason: strings.TrimSpace(v.AdmitReason),
				EjectID:     ejectID,
				EjectReason: strings.TrimSpace(v.EjectReason),
			}
			return nil
		})
	}
	if err := gVotes.Wait(); err != nil {
		return err
	}

	// Step 4: Tally ballots fairly (no alphabetical tie-breaking, minimum support floor).
	admitVotes := map[string]int{}
	ejectVotes := map[string]int{}
	totalAdmitBallots := 0
	for _, b := range ballots {
		if b.AdmitID != "none" {
			admitVotes[b.AdmitID]++
			totalAdmitBallots++
		}
		if b.EjectID != "" {
			ejectVotes[b.EjectID]++
		}
	}

	now := time.Now().UTC()
	var admittedIDs []string
	ejectedIDs := append([]string(nil), retentionEjected...)
	var summaryParts []string

	// Record LastAppliedIssue for any returning alumnus who pitched so rotation stays fair.
	if returningAlumnus != nil {
		returningAlumnus.LastAppliedIssue = state.CurrentIssue
		if err := e.store.SaveAgent(ctx, *returningAlumnus); err != nil {
			return err
		}
	}

	// Require >50% voting to admit someone AND top candidate reaching at least ~40% of collective (min 2 votes).
	minWinnerVotes := max(2, (len(activeWriters)*2+4)/5)
	winnerID := pickFairWinner(admitVotes)
	if totalAdmitBallots > len(activeWriters)/2 && winnerID != "" &&
		admitVotes[winnerID] >= minWinnerVotes {
		winner := appMap[winnerID]
		admittedIDs = append(admittedIDs, winner.ID)

		if atCap {
			ejectID := pickEjectionTarget(ejectVotes, wMap)
			if ejectID != "" {
				ejected := wMap[ejectID]
				ejected.Status = model.StatusEjected
				ejected.EjectedIssue = state.CurrentIssue
				ejected.Notebook = strings.TrimSpace(
					ejected.Notebook + fmt.Sprintf(
						" Ejected from Cornice after Issue %d to make room for a new member.",
						state.CurrentIssue,
					),
				)
				if err := e.store.SaveAgent(ctx, ejected); err != nil {
					return err
				}
				ejectedIDs = append(ejectedIDs, ejected.ID)
				summaryParts = append(
					summaryParts,
					fmt.Sprintf(
						"%s was ejected to make room (%d votes).",
						ejected.Name,
						ejectVotes[ejectID],
					),
				)
				log.Printf(
					"  -> EJECTED: %s (%d votes)",
					ejected.Name,
					ejectVotes[ejectID],
				)
			}
		}

		var newAgent model.Agent
		if winner.IsReturningAlumni {
			existing, err := e.store.GetAgent(ctx, winner.ID)
			if err != nil {
				return fmt.Errorf(
					"load returning alumnus %s: %w",
					winner.ID,
					err,
				)
			}
			newAgent = existing
			newAgent.Status = model.StatusActive
			newAgent.JoinedIssue = state.CurrentIssue
			// Mark latest pre-ejection story so readmitted writer gets a fresh runway before any new retention trigger.
			if len(newAgent.Standing) > 0 {
				newAgent.LastRetentionStoryID = newAgent.Standing[len(newAgent.Standing)-1].StoryID
			}
			newAgent.Notebook = strings.TrimSpace(
				newAgent.Notebook + fmt.Sprintf(
					" Readmitted to Cornice after Issue %d.",
					state.CurrentIssue,
				),
			)
		} else {
			newAgent = model.Agent{
				ID:                   winner.ID,
				Name:                 winner.Name,
				Role:                 model.RoleWriter,
				Status:               model.StatusActive,
				Genre:                winner.Genre,
				Bio:                  winner.Bio,
				VoiceCraft:           winner.VoiceCraft,
				NegativeRules:        winner.NegativeRules,
				EditorialTemperament: winner.EditorialTemperament,
				Notebook:             fmt.Sprintf("Admitted to Cornice after Issue %d via Open Call. Eager to establish my voice in the collective.", state.CurrentIssue),
				Relationships:        map[string]model.Relationship{},
				JoinedIssue:          state.CurrentIssue,
				CreatedAt:            now,
			}
		}
		if err := e.store.SaveAgent(ctx, newAgent); err != nil {
			return err
		}
		state.AuthorQueue = append(
			[]string{newAgent.ID},
			removeID(state.AuthorQueue, newAgent.ID)...)
		for _, ej := range ejectedIDs {
			state.AuthorQueue = removeID(state.AuthorQueue, ej)
		}
		summaryParts = append(
			[]string{
				fmt.Sprintf(
					"%s (%s) was admitted to the collective (%d/%d votes).",
					winner.Name,
					winner.Genre,
					admitVotes[winner.ID],
					len(activeWriters),
				),
			},
			summaryParts...)
		log.Printf(
			"  -> ADMITTED: %s (%s) with %d votes",
			winner.Name,
			winner.Genre,
			admitVotes[winner.ID],
		)
	} else {
		summaryParts = append(summaryParts, fmt.Sprintf("No applicant secured sufficient support (%d/%d voted to admit). Roster unchanged.", totalAdmitBallots, len(activeWriters)))
		log.Printf("  -> NO ADMISSION (%d/%d voted to admit)", totalAdmitBallots, len(activeWriters))
	}

	for _, m := range motions {
		summaryParts = append(summaryParts, m.Summary)
	}

	ocID, err := e.nextOpenCallID(ctx, state.CurrentIssue)
	if err != nil {
		return err
	}
	oc := model.OpenCall{
		ID:               ocID,
		AfterIssue:       state.CurrentIssue,
		Applicants:       applicants,
		Ballots:          ballots,
		AdmittedIDs:      admittedIDs,
		EjectedIDs:       ejectedIDs,
		RetentionMotions: motions,
		Summary:          strings.Join(summaryParts, " "),
		ResolvedAt:       now,
	}
	if err := e.store.SaveOpenCall(ctx, oc); err != nil {
		return err
	}

	if advancePhase {
		state.NextPhase = model.PhaseCommission
	}
	return nil
}

func (e *Engine) runRetentionVotes(
	ctx context.Context,
	state *model.State,
	archiveSnap *adkrun.ArchiveSnapshot,
) ([]model.RetentionMotion, []string, error) {
	retVoteSkill, err := e.mustSkill("vote-retention")
	if err != nil {
		return nil, nil, err
	}

	allAgents, err := e.store.ListAgents(ctx)
	if err != nil {
		return nil, nil, err
	}
	activeWriters := filterAgents(
		allAgents,
		model.RoleWriter,
		model.StatusActive,
	)

	var flagged []struct {
		agent         model.Agent
		reason        string
		latestStoryID string
	}
	for _, w := range activeWriters {
		if len(w.Standing) < 2 {
			continue
		}
		lastTwo := w.Standing[len(w.Standing)-2:]
		latestID := lastTwo[1].StoryID
		// Prevent "Groundhog Day" retrial on the exact same two stories before the writer gets a new turn.
		if latestID != "" && latestID == w.LastRetentionStoryID {
			continue
		}
		avg := standingWindowScore(lastTwo)
		if avg <= 2.0 {
			reason := fmt.Sprintf(
				"Averaged %.1f / 4 across last two published pieces (Issue %d \"%s\" and Issue %d \"%s\").",
				avg,
				lastTwo[0].IssueNumber,
				lastTwo[0].StoryTitle,
				lastTwo[1].IssueNumber,
				lastTwo[1].StoryTitle,
			)
			flagged = append(flagged, struct {
				agent         model.Agent
				reason        string
				latestStoryID string
			}{agent: w, reason: reason, latestStoryID: latestID})
		}
	}
	if len(flagged) == 0 {
		return nil, nil, nil
	}

	var motions []model.RetentionMotion
	var newlyEjected []string
	ejectedSet := map[string]bool{}

	for _, item := range flagged {
		target := item.agent
		log.Printf(
			"[retention] Convening retention vote for %s (%s)",
			target.Name,
			item.reason,
		)

		var voters []model.Agent
		for _, w := range activeWriters {
			if w.ID != target.ID && !ejectedSet[w.ID] {
				voters = append(voters, w)
			}
		}

		ballots := make([]model.RetentionBallot, len(voters))
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(2)
		for i, v := range voters {
			idx, voter := i, v
			g.Go(func() error {
				archive, err := archiveSnap.NewSession(
					voter.Name,
					e.cfg.MaxArchiveReads,
				)
				if err != nil {
					return err
				}
				res, err := adkrun.RunAction(gctx, e.adk, adkrun.Request{
					AgentName: voter.ID + "_retention",
					Instruction: prompts.AgentInstruction(
						state.Charter,
						voter,
						retVoteSkill,
					),
					UserMessage: prompts.RetentionVoteInput(
						voter,
						target,
						item.reason,
						archive.Register,
					),
					Temperature: 0.5,
					Tools:       archive.Tools,
					BeforeToolCallbacks: []llmagent.BeforeToolCallback{
						archive.Guard,
					},
					OnAttemptReset: archive.Reset,
				}, "cast_retention_ballot", "Cast a 'retain' or 'eject' ballot for the flagged collective member.",
					func(a retentionBallotArgs) error {
						vote := strings.ToLower(strings.TrimSpace(a.Vote))
						if vote != "retain" && vote != "eject" {
							return fmt.Errorf(
								"vote must be strictly 'retain' or 'eject', got %q",
								a.Vote,
							)
						}
						return nil
					})
				if err != nil {
					return fmt.Errorf(
						"retention ballot by %s on %s: %w",
						voter.Name,
						target.Name,
						err,
					)
				}
				vote := strings.ToLower(strings.TrimSpace(res.Vote))
				ballots[idx] = model.RetentionBallot{
					VoterID:   voter.ID,
					VoterName: voter.Name,
					Vote:      vote,
					Reason:    strings.TrimSpace(res.Reason),
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, nil, err
		}

		retainVotes, ejectVotes := 0, 0
		for _, b := range ballots {
			if b.Vote == "eject" {
				ejectVotes++
			} else {
				retainVotes++
			}
		}

		ejected := ejectVotes > retainVotes
		target.LastRetentionStoryID = item.latestStoryID
		var summary string
		if ejected {
			target.Status = model.StatusEjected
			target.EjectedIssue = state.CurrentIssue
			target.Notebook = strings.TrimSpace(
				target.Notebook + fmt.Sprintf(
					" Ejected by retention vote (%d-%d) after Issue %d following consecutive weak reviews.",
					ejectVotes,
					retainVotes,
					state.CurrentIssue,
				),
			)
			state.AuthorQueue = removeID(state.AuthorQueue, target.ID)
			newlyEjected = append(newlyEjected, target.ID)
			ejectedSet[target.ID] = true
			summary = fmt.Sprintf(
				"%s was ejected by retention vote (%d eject, %d retain).",
				target.Name,
				ejectVotes,
				retainVotes,
			)
			log.Printf(
				"  -> EJECTED %s (%d-%d)",
				target.Name,
				ejectVotes,
				retainVotes,
			)
		} else {
			// Record trust fallout toward peers who voted to eject a surviving member.
			for _, b := range ballots {
				if b.Vote == "eject" {
					applyRelationshipDelta(&target, b.VoterID, -1, 0, fmt.Sprintf("Voted to eject me in the retention vote after Issue %d.", state.CurrentIssue))
				}
			}
			target.Notebook = strings.TrimSpace(target.Notebook + fmt.Sprintf(" Survived a retention vote (%d retain, %d eject) after Issue %d; my next piece must answer the critics.", retainVotes, ejectVotes, state.CurrentIssue))
			summary = fmt.Sprintf("%s was retained (%d retain, %d eject).", target.Name, retainVotes, ejectVotes)
			log.Printf("  -> RETAINED %s (%d-%d)", target.Name, retainVotes, ejectVotes)
		}
		if err := e.store.SaveAgent(ctx, target); err != nil {
			return nil, nil, err
		}

		motions = append(motions, model.RetentionMotion{
			TargetID:      target.ID,
			TargetName:    target.Name,
			TriggerReason: item.reason,
			Ballots:       ballots,
			RetainVotes:   retainVotes,
			EjectVotes:    ejectVotes,
			Ejected:       ejected,
			Summary:       summary,
		})
	}
	return motions, newlyEjected, nil
}

func (e *Engine) nextOpenCallID(
	ctx context.Context,
	afterIssue int,
) (string, error) {
	base := fmt.Sprintf("open-call-after-issue-%04d", afterIssue)
	existing, err := e.store.ListOpenCalls(ctx)
	if err != nil {
		return base, err
	}
	used := map[string]bool{}
	for _, oc := range existing {
		used[oc.ID] = true
	}
	if !used[base] {
		return base, nil
	}
	for suffix := 2; ; suffix++ {
		cand := fmt.Sprintf("%s-%d", base, suffix)
		if !used[cand] {
			return cand, nil
		}
	}
}

func standingWindowScore(entries []model.StandingEntry) float64 {
	points := map[string]float64{
		"Acclaimed": 4,
		"Commended": 3,
		"Mixed":     2,
		"Panned":    1,
	}
	var sum float64
	var count int
	for _, e := range entries {
		for _, v := range e.Verdicts {
			if p, ok := points[v]; ok {
				sum += p
				count++
			}
		}
	}
	if count == 0 {
		return 3.0
	}
	return sum / float64(count)
}

func (e *Engine) recentIssuesDigest(ctx context.Context, limit int) string {
	issues, err := e.store.ListIssues(ctx)
	if err != nil || len(issues) == 0 {
		return ""
	}
	var b strings.Builder
	count := 0
	for _, iss := range issues {
		if len(iss.Stories) == 0 {
			continue
		}
		fmt.Fprintf(&b, "Issue %d:\n", iss.Number)
		for _, s := range iss.Stories {
			fmt.Fprintf(
				&b,
				"  - \"%s\" by %s (%s): %s\n",
				s.Title,
				s.AuthorName,
				s.Genre,
				s.Epigraph,
			)
		}
		count++
		if count >= limit {
			break
		}
	}
	return b.String()
}

// pickFairWinner selects the highest-voted candidate, breaking ties randomly (never alphabetically).
func pickFairWinner(counts map[string]int) string {
	bestVal := 0
	var tied []string
	for k, v := range counts {
		if v > bestVal {
			bestVal = v
			tied = []string{k}
		} else if v == bestVal && v > 0 {
			tied = append(tied, k)
		}
	}
	if len(tied) == 0 {
		return ""
	}
	return tied[rand.IntN(len(tied))]
}

// pickEjectionTarget selects the highest-voted ejection target; breaks ties by lowest recent standing, then randomly.
func pickEjectionTarget(
	counts map[string]int,
	members map[string]model.Agent,
) string {
	bestVal := 0
	var tied []string
	for k, v := range counts {
		if v > bestVal {
			bestVal = v
			tied = []string{k}
		} else if v == bestVal && v > 0 {
			tied = append(tied, k)
		}
	}
	if len(tied) == 0 {
		return ""
	}
	if len(tied) == 1 {
		return tied[0]
	}
	lowestStanding := 99.0
	var worst []string
	for _, id := range tied {
		score := standingWindowScore(members[id].Standing)
		if score < lowestStanding {
			lowestStanding = score
			worst = []string{id}
		} else if score == lowestStanding {
			worst = append(worst, id)
		}
	}
	return worst[rand.IntN(len(worst))]
}

func removeID(slice []string, target string) []string {
	var out []string
	for _, s := range slice {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}
