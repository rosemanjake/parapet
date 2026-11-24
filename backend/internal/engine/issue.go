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
	"cornice/internal/media"
	"cornice/internal/model"
	"cornice/internal/prompts"
)

type commissionArgs struct {
	Title   string `json:"title"   jsonschema:"Understated, memorable literary title for the story"`
	Premise string `json:"premise" jsonschema:"2-3 crisp sentences describing the setup and tension"`
	Outline string `json:"outline" jsonschema:"4-6 beat narrative outline with character names, material stakes, and ending"`
}

func (e *Engine) runCommission(
	ctx context.Context,
	state *model.State,
	storiesPerIssue int,
) error {
	skill, err := e.mustSkill("commission-story")
	if err != nil {
		return err
	}
	allAgents, err := e.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	writers := filterAgents(allAgents, model.RoleWriter, model.StatusActive)
	if len(writers) == 0 {
		return fmt.Errorf("no active writers available")
	}
	wMap := agentMap(writers)

	var cleanQueue []string
	seen := map[string]bool{}
	for _, id := range state.AuthorQueue {
		if _, ok := wMap[id]; ok && !seen[id] {
			cleanQueue = append(cleanQueue, id)
			seen[id] = true
		}
	}
	for _, w := range writers {
		if !seen[w.ID] {
			cleanQueue = append(cleanQueue, w.ID)
			seen[w.ID] = true
		}
	}

	count := storiesPerIssue
	if count > len(cleanQueue) {
		count = len(cleanQueue)
	}
	chosenIDs := append([]string(nil), cleanQueue[:count]...)
	nextIssue := state.CurrentIssue + 1

	// Idempotency guard: if a previous commission attempt saved some stories for nextIssue
	// before failing, reuse those stories rather than creating orphaned duplicates.
	existingStories, err := e.store.ListStoriesByIssue(ctx, nextIssue)
	if err != nil {
		return err
	}
	alreadyByAuthor := map[string]bool{}
	for _, st := range existingStories {
		alreadyByAuthor[st.AuthorID] = true
	}

	// Load the archive once for all authors commissioned in this tick.
	archiveSnap, err := adkrun.LoadArchiveSnapshot(ctx, e.store)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for idx, authorID := range chosenIDs {
		author := wMap[authorID]
		if alreadyByAuthor[author.ID] {
			log.Printf(
				"[commission] Issue %d: skipping %s (already commissioned on prior attempt)",
				nextIssue,
				author.Name,
			)
			continue
		}
		log.Printf(
			"[commission] Issue %d: %s (%s)",
			nextIssue,
			author.Name,
			author.Genre,
		)

		archive, err := archiveSnap.NewSession(
			author.Name,
			e.cfg.MaxArchiveReads,
		)
		if err != nil {
			return err
		}

		res, err := adkrun.RunAction(ctx, e.adk, adkrun.Request{
			AgentName: author.ID + "_commission",
			Instruction: prompts.AgentInstruction(
				state.Charter,
				author,
				skill,
			),
			UserMessage: prompts.CommissionInput(
				nextIssue,
				archive.Register,
			),
			Temperature:         0.9,
			Tools:               archive.Tools,
			BeforeToolCallbacks: []llmagent.BeforeToolCallback{archive.Guard},
			OnAttemptReset:      archive.Reset,
		}, "submit_commission", "Submit your commissioned story title, premise, and beat outline.",
			func(a commissionArgs) error {
				if strings.TrimSpace(a.Title) == "" ||
					strings.TrimSpace(a.Premise) == "" ||
					strings.TrimSpace(a.Outline) == "" {
					return fmt.Errorf(
						"title, premise, and outline must all be non-empty",
					)
				}
				return nil
			})
		if err != nil {
			return fmt.Errorf("commission for %s: %w", author.Name, err)
		}

		storyID := uniqueStoryID(nextIssue, res.Title, author.ID)
		st := model.Story{
			ID:          storyID,
			Slug:        storyID,
			IssueNumber: nextIssue,
			Status:      "commissioned",
			AuthorID:    author.ID,
			AuthorName:  author.Name,
			Genre:       author.Genre,
			Title:       strings.TrimSpace(res.Title),
			Premise:     strings.TrimSpace(res.Premise),
			Outline:     strings.TrimSpace(res.Outline),
			Consulted:   archive.Consulted(),
			CreatedAt:   now.Add(time.Duration(idx) * time.Second),
		}
		if err := e.store.SaveStory(ctx, st); err != nil {
			return err
		}
		log.Printf(
			"  -> commissioned \"%s\" (%s) [archive reads: %d]",
			st.Title,
			st.ID,
			len(st.Consulted),
		)
	}

	issue := model.Issue{
		Number: nextIssue,
		Status: "commissioning",
	}
	if err := e.store.SaveIssue(ctx, issue); err != nil {
		return err
	}
	state.CurrentIssue = nextIssue
	state.AuthorQueue = append(cleanQueue[count:], chosenIDs...)
	state.NextPhase = model.PhaseDraft
	return nil
}

func (e *Engine) runDraft(ctx context.Context, state *model.State) error {
	skill, err := e.mustSkill("draft-story")
	if err != nil {
		return err
	}
	stories, err := e.store.ListStoriesByIssue(ctx, state.CurrentIssue)
	if err != nil {
		return err
	}
	allAgents, err := e.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	aMap := agentMap(allAgents)

	g, gctx := errgroup.WithContext(ctx)
	for i := range stories {
		st := stories[i]
		if st.Status != "commissioned" {
			continue
		}
		author := aMap[st.AuthorID]
		g.Go(func() error {
			log.Printf("[draft] %s drafting \"%s\"...", author.Name, st.Title)
			draft, err := e.adk.RunText(gctx, adkrun.Request{
				AgentName: author.ID + "_draft",
				Instruction: prompts.AgentInstruction(
					state.Charter,
					author,
					skill,
				),
				UserMessage: prompts.DraftInput(st),
				Temperature: 0.9,
			})
			if err != nil {
				return fmt.Errorf("draft %s: %w", st.ID, err)
			}
			st.DraftV1 = draft
			st.Status = "drafted"
			if err := e.store.SaveStory(gctx, st); err != nil {
				return err
			}
			log.Printf(
				"  -> finished draft \"%s\" (%d words)",
				st.Title,
				len(strings.Fields(draft)),
			)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	issue, err := e.store.GetIssue(ctx, state.CurrentIssue)
	if err != nil {
		return fmt.Errorf("load issue %d: %w", state.CurrentIssue, err)
	}
	issue.Status = "drafting"
	if err := e.store.SaveIssue(ctx, issue); err != nil {
		return fmt.Errorf("save issue %d status: %w", state.CurrentIssue, err)
	}
	state.NextPhase = model.PhaseWorkshop
	return nil
}

type chooseEditorArgs struct {
	EditorID string `json:"editor_id" jsonschema:"Exact ID of the chosen peer editor from the available peers list"`
	Reason   string `json:"reason"    jsonschema:"1-2 sentences in your own voice explaining why you chose this editor"`
}

type reviseMetaArgs struct {
	Title             string `json:"title"              jsonschema:"Final published title of the story"`
	Synopsis          string `json:"synopsis"           jsonschema:"One plain, concrete, non-spoiler sentence (under 24 words) stating what the story is about"`
	ImageScene        string `json:"image_scene"        jsonschema:"One quiet, concrete physical scene or landscape moment from the story for a 35mm editorial headline photograph (no text, no faces posing)"`
	RevisionNote      string `json:"revision_note"      jsonschema:"2-4 sentences explaining what you took from the editor's notes and what you pushed back on"`
	TrustDelta        int    `json:"trust_delta"        jsonschema:"Integer from -2 to +2 reflecting change in trust toward the editor"`
	RespectDelta      int    `json:"respect_delta"      jsonschema:"Integer from -2 to +2 reflecting change in literary respect for the editor"`
	UpdatedImpression string `json:"updated_impression" jsonschema:"1-2 sentences capturing your updated private impression of the editor"`
}

func (e *Engine) runWorkshop(ctx context.Context, state *model.State) error {
	chooseSkill, err := e.mustSkill("choose-editor")
	if err != nil {
		return err
	}
	editSkill, err := e.mustSkill("workshop-edit")
	if err != nil {
		return err
	}
	reviseSkill, err := e.mustSkill("revise-story")
	if err != nil {
		return err
	}
	metaSkill, err := e.mustSkill("revise-metadata")
	if err != nil {
		return err
	}

	stories, err := e.store.ListStoriesByIssue(ctx, state.CurrentIssue)
	if err != nil {
		return err
	}
	allAgents, err := e.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	writers := filterAgents(allAgents, model.RoleWriter, model.StatusActive)
	aMap := agentMap(allAgents)

	for _, st := range stories {
		if st.Status != "drafted" {
			continue
		}
		author := aMap[st.AuthorID]
		var peers []model.Agent
		peerSet := map[string]bool{}
		for _, w := range writers {
			if w.ID != author.ID {
				peers = append(peers, w)
				peerSet[w.ID] = true
			}
		}
		if len(peers) == 0 {
			st.FinalText = st.DraftV1
			st.Status = "workshopped"
			if err := e.store.SaveStory(ctx, st); err != nil {
				return err
			}
			continue
		}

		// Step 1: Author calls select_editor tool.
		chosen, err := adkrun.RunAction(ctx, e.adk, adkrun.Request{
			AgentName: author.ID + "_choose_editor",
			Instruction: prompts.AgentInstruction(
				state.Charter,
				author,
				chooseSkill,
			),
			UserMessage: prompts.ChooseEditorInput(author, st, peers),
			Temperature: 0.5,
		}, "select_editor", "Select one peer editor from the available peers list.",
			func(a chooseEditorArgs) error {
				if !peerSet[strings.TrimSpace(a.EditorID)] {
					return fmt.Errorf(
						"editor_id %q is not in the available peers list",
						a.EditorID,
					)
				}
				return nil
			})
		if err != nil {
			return fmt.Errorf("choose editor for %s: %w", st.ID, err)
		}
		editor := aMap[strings.TrimSpace(chosen.EditorID)]
		st.EditorID = editor.ID
		st.EditorName = editor.Name
		st.EditorSelectReason = strings.TrimSpace(chosen.Reason)
		log.Printf(
			"[workshop] %s invited %s to edit \"%s\"",
			author.Name,
			editor.Name,
			st.Title,
		)

		// Step 2: Peer editor writes blunt workshop notes (freeform prose).
		notes, err := e.adk.RunText(ctx, adkrun.Request{
			AgentName: editor.ID + "_workshop_edit",
			Instruction: prompts.AgentInstruction(
				state.Charter,
				editor,
				editSkill,
			),
			UserMessage: prompts.WorkshopCritiqueInput(editor, author, st),
			Temperature: 0.75,
		})
		if err != nil {
			return fmt.Errorf("workshop critique for %s: %w", st.ID, err)
		}
		st.EditorNotes = notes

		// Step 3a: Author writes full revised prose via RunText (never inside a tool argument).
		draftWords := len(strings.Fields(st.DraftV1))
		minWords := max(600, draftWords/2)
		var finalText string
		for attempt := 0; attempt < 2; attempt++ {
			candidate, err := e.adk.RunText(ctx, adkrun.Request{
				AgentName: author.ID + "_revise",
				Instruction: prompts.AgentInstruction(
					state.Charter,
					author,
					reviseSkill,
				),
				UserMessage: prompts.ReviseStoryInput(editor, st),
				Temperature: 0.85,
			})
			if err != nil {
				return fmt.Errorf("revise prose %s: %w", st.ID, err)
			}
			if len(strings.Fields(candidate)) >= minWords {
				finalText = candidate
				break
			}
			log.Printf(
				"  ! revision attempt %d too short (%d words), retrying...",
				attempt+1,
				len(strings.Fields(candidate)),
			)
		}
		if finalText == "" {
			log.Printf(
				"  ! falling back to draft_v1 for %s after short revisions",
				st.ID,
			)
			finalText = st.DraftV1
		}
		st.FinalText = strings.TrimSpace(finalText)

		// Step 3b: Author calls submit_revision_metadata tool.
		rev, err := adkrun.RunAction(ctx, e.adk, adkrun.Request{
			AgentName: author.ID + "_revise_meta",
			Instruction: prompts.AgentInstruction(
				state.Charter,
				author,
				metaSkill,
			),
			UserMessage: prompts.ReviseMetadataInput(author, editor, st),
			Temperature: 0.5,
		}, "submit_revision_metadata", "Submit final title, one-sentence synopsis, headline photograph scene, revision note, and relationship update toward the editor.",
			func(a reviseMetaArgs) error {
				if strings.TrimSpace(a.Title) == "" ||
					strings.TrimSpace(a.Synopsis) == "" ||
					strings.TrimSpace(a.ImageScene) == "" ||
					strings.TrimSpace(a.RevisionNote) == "" {
					return fmt.Errorf(
						"title, synopsis, image_scene, and revision_note must all be non-empty",
					)
				}
				return nil
			})
		if err != nil {
			return fmt.Errorf("revise metadata %s: %w", st.ID, err)
		}
		st.Title = strings.TrimSpace(rev.Title)
		st.Synopsis = strings.TrimSpace(rev.Synopsis)
		st.ImagePrompt = media.BuildPrompt(rev.ImageScene)
		st.AuthorRevisionNote = strings.TrimSpace(rev.RevisionNote)
		st.Status = "workshopped"

		applyRelationshipDelta(
			&author,
			editor.ID,
			rev.TrustDelta,
			rev.RespectDelta,
			rev.UpdatedImpression,
		)
		aMap[author.ID] = author

		if err := e.store.SaveStory(ctx, st); err != nil {
			return err
		}
		if err := e.store.SaveAgent(ctx, author); err != nil {
			return err
		}
		log.Printf(
			"  -> revised \"%s\" (%d words)",
			st.Title,
			len(strings.Fields(st.FinalText)),
		)
	}

	issue, err := e.store.GetIssue(ctx, state.CurrentIssue)
	if err != nil {
		return fmt.Errorf("load issue %d: %w", state.CurrentIssue, err)
	}
	issue.Status = "workshop"
	if err := e.store.SaveIssue(ctx, issue); err != nil {
		return fmt.Errorf("save issue %d status: %w", state.CurrentIssue, err)
	}
	state.NextPhase = model.PhasePublish
	return nil
}

func (e *Engine) runPublish(ctx context.Context, state *model.State) error {
	stories, err := e.store.ListStoriesByIssue(ctx, state.CurrentIssue)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var summaries []model.StorySummary
	for _, st := range stories {
		if st.FinalText == "" {
			st.FinalText = st.DraftV1
		}
		if err := e.ensureEditorialAssets(ctx, state.Charter, &st); err != nil {
			return fmt.Errorf("editorial assets for %s: %w", st.ID, err)
		}
		st.Status = "published"
		if st.PublishedAt.IsZero() {
			st.PublishedAt = now
		}
		if err := e.store.SaveStory(ctx, st); err != nil {
			return err
		}
		summaries = append(summaries, model.StorySummary{
			ID:         st.ID,
			Slug:       st.Slug,
			Title:      st.Title,
			AuthorID:   st.AuthorID,
			AuthorName: st.AuthorName,
			Genre:      st.Genre,
			Synopsis:   st.Synopsis,
			Epigraph:   st.Epigraph,
			ImageURL:   st.ImageURL,
		})
	}

	issue := model.Issue{
		Number:      state.CurrentIssue,
		Status:      "published",
		PublishedAt: now,
		Stories:     summaries,
	}
	if err := e.store.SaveIssue(ctx, issue); err != nil {
		return err
	}
	log.Printf(
		"[publish] Issue %d is live with %d stories",
		issue.Number,
		len(summaries),
	)
	state.NextPhase = model.PhaseReview
	return nil
}

type salonCommentArgs struct {
	Comment           string `json:"comment"            jsonschema:"70-140 word published salon comment in your own voice"`
	RespectDelta      int    `json:"respect_delta"      jsonschema:"Integer from -2 to +2 reflecting change in respect for the author"`
	UpdatedImpression string `json:"updated_impression" jsonschema:"1-2 sentences updating your private view of the author"`
}

type reviewEntryArgs struct {
	StoryID string `json:"story_id" jsonschema:"Exact story_id from the assigned review packet"`
	Rank    int    `json:"rank"     jsonschema:"Unique comparative rank from 1 (best) to N (weakest); set 1 if only 1 piece"`
	Verdict string `json:"verdict"  jsonschema:"Strictly one of: Acclaimed, Commended, Mixed, Panned"`
	Body    string `json:"body"     jsonschema:"Signed 180-300 word critical review citing specific passages or structural turns"`
}

type submitReviewsArgs struct {
	Reviews []reviewEntryArgs `json:"reviews" jsonschema:"One review entry per assigned story_id"`
}

func (e *Engine) runReview(ctx context.Context, state *model.State) error {
	salonSkill, err := e.mustSkill("salon-comment")
	if err != nil {
		return err
	}
	reviewSkill, err := e.mustSkill("review-issue")
	if err != nil {
		return err
	}

	stories, err := e.store.ListStoriesByIssue(ctx, state.CurrentIssue)
	if err != nil {
		return err
	}
	allAgents, err := e.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	writers := filterAgents(allAgents, model.RoleWriter, model.StatusActive)
	critics := filterAgents(allAgents, model.RoleCritic, model.StatusActive)
	aMap := agentMap(allAgents)

	now := time.Now().UTC()

	storyByID := make(map[string]*model.Story, len(stories))
	for i := range stories {
		storyByID[stories[i].ID] = &stories[i]
	}

	// Part 1: Peer salon comments (idempotent: skips peers who already commented on a story).
	for i := range stories {
		st := &stories[i]
		existingCommenters := map[string]bool{}
		for _, c := range st.SalonComments {
			existingCommenters[c.AuthorID] = true
		}

		var commenters []model.Agent
		for _, w := range writers {
			if w.ID != st.AuthorID {
				commenters = append(commenters, w)
			}
		}
		if len(commenters) > 5 {
			offset := (state.CurrentIssue + i) % len(commenters)
			rotated := append(commenters[offset:], commenters[:offset]...)
			commenters = rotated[:5]
		}

		for _, reader := range commenters {
			if existingCommenters[reader.ID] {
				continue
			}
			readerCur := aMap[reader.ID]
			log.Printf(
				"[salon] %s commenting on \"%s\"",
				readerCur.Name,
				st.Title,
			)
			res, err := adkrun.RunAction(ctx, e.adk, adkrun.Request{
				AgentName: readerCur.ID + "_salon",
				Instruction: prompts.AgentInstruction(
					state.Charter,
					readerCur,
					salonSkill,
				),
				UserMessage: prompts.SalonCommentInput(readerCur, *st),
				Temperature: 0.75,
			}, "post_salon_comment", "Post your salon comment and update your private view of the author.",
				func(a salonCommentArgs) error {
					if strings.TrimSpace(a.Comment) == "" {
						return fmt.Errorf("comment must not be empty")
					}
					return nil
				})
			if err != nil {
				return fmt.Errorf(
					"salon comment by %s on %s: %w",
					readerCur.Name,
					st.ID,
					err,
				)
			}
			st.SalonComments = append(st.SalonComments, model.SalonComment{
				AuthorID:    readerCur.ID,
				AuthorName:  readerCur.Name,
				AuthorGenre: readerCur.Genre,
				Body:        strings.TrimSpace(res.Comment),
				CreatedAt:   now,
			})
			applyRelationshipDelta(
				&readerCur,
				st.AuthorID,
				0,
				res.RespectDelta,
				res.UpdatedImpression,
			)
			aMap[readerCur.ID] = readerCur
			// Save incrementally so a later error does not cause duplicate comments on retry.
			if err := e.store.SaveStory(ctx, *st); err != nil {
				return err
			}
			if err := e.store.SaveAgent(ctx, readerCur); err != nil {
				return err
			}
		}
	}

	// Part 2: Select ONE shared 5-critic panel for the entire Issue (rotated evenly across the 10-critic bench).
	// Reading the full issue together preserves true comparative ranking and enforces the <=1 Acclaimed cap.
	const panelSize = 5
	allStories, err := e.store.ListStories(ctx)
	if err != nil {
		return fmt.Errorf("list stories for critic balancing: %w", err)
	}
	reviewCounts := map[string]int{}
	existingCriticsOnIssue := map[string]bool{}
	for _, s := range allStories {
		for _, r := range s.Reviews {
			reviewCounts[r.CriticID]++
			if s.IssueNumber == state.CurrentIssue {
				existingCriticsOnIssue[r.CriticID] = true
			}
		}
	}

	// If a prior attempt already assigned some critics on this issue, keep them and fill up to panelSize.
	var panel []model.Agent
	panelSet := map[string]bool{}
	for _, c := range critics {
		if existingCriticsOnIssue[c.ID] {
			panel = append(panel, c)
			panelSet[c.ID] = true
		}
	}
	if len(panel) < panelSize {
		var remaining []model.Agent
		for _, c := range critics {
			if !panelSet[c.ID] {
				remaining = append(remaining, c)
			}
		}
		needed := panelSize - len(panel)
		panel = append(
			panel,
			selectBalancedCritics(remaining, reviewCounts, needed)...)
	}

	var panelNames []string
	for _, c := range panel {
		panelNames = append(panelNames, c.Name)
	}
	log.Printf(
		"[review] Issue %d shared critic panel (%d): %s",
		state.CurrentIssue,
		len(panel),
		strings.Join(panelNames, ", "),
	)

	archiveSnap, err := adkrun.LoadArchiveSnapshot(ctx, e.store)
	if err != nil {
		return err
	}

	validVerdicts := map[string]bool{
		"Acclaimed": true, "Commended": true, "Mixed": true, "Panned": true,
	}
	expectedIDs := map[string]bool{}
	for _, s := range stories {
		expectedIDs[s.ID] = true
	}

	// Filter out critics who already reviewed every story in this issue (idempotent retry).
	var pendingPanel []model.Agent
	for _, c := range panel {
		reviewedAll := true
		for _, s := range stories {
			if !storyHasReviewBy(s, c.ID) {
				reviewedAll = false
				break
			}
		}
		if !reviewedAll {
			pendingPanel = append(pendingPanel, c)
		}
	}

	bundles := make([]submitReviewsArgs, len(pendingPanel))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(2)
	for i, c := range pendingPanel {
		idx, critic := i, c
		g.Go(func() error {
			log.Printf(
				"[review] Critic %s reviewing Issue %d (%d pieces)",
				critic.Name,
				state.CurrentIssue,
				len(stories),
			)
			archive, err := archiveSnap.NewSession(critic.Name, 2)
			if err != nil {
				return err
			}
			bundle, err := adkrun.RunAction(gctx, e.adk, adkrun.Request{
				AgentName: critic.ID + "_review",
				Instruction: prompts.AgentInstruction(
					state.Charter,
					critic,
					reviewSkill,
				),
				UserMessage: prompts.CriticIssueReviewInput(
					state.CurrentIssue,
					stories,
					archive.Register,
				),
				Temperature: 0.6,
				Tools:       archive.Tools,
				BeforeToolCallbacks: []llmagent.BeforeToolCallback{
					archive.Guard,
				},
				OnAttemptReset: archive.Reset,
			}, "submit_reviews", "Submit your signed critical reviews, unique comparative ranks, and verdicts for all pieces in this issue.",
				func(a submitReviewsArgs) error {
					if len(a.Reviews) != len(stories) {
						return fmt.Errorf(
							"expected %d reviews, got %d",
							len(stories),
							len(a.Reviews),
						)
					}
					seenStory := map[string]bool{}
					seenRank := map[int]bool{}
					acclaimed := 0
					for _, r := range a.Reviews {
						if !expectedIDs[r.StoryID] {
							return fmt.Errorf("unknown story_id %q", r.StoryID)
						}
						if seenStory[r.StoryID] {
							return fmt.Errorf(
								"duplicate review for story_id %q",
								r.StoryID,
							)
						}
						seenStory[r.StoryID] = true
						if r.Rank < 1 || r.Rank > len(stories) {
							return fmt.Errorf(
								"rank for %q must be between 1 and %d, got %d",
								r.StoryID,
								len(stories),
								r.Rank,
							)
						}
						if seenRank[r.Rank] {
							return fmt.Errorf(
								"duplicate rank %d; ranks must be unique 1..%d with no ties",
								r.Rank,
								len(stories),
							)
						}
						seenRank[r.Rank] = true
						if !validVerdicts[r.Verdict] {
							return fmt.Errorf(
								"invalid verdict %q; must be Acclaimed, Commended, Mixed, or Panned",
								r.Verdict,
							)
						}
						if r.Verdict == "Acclaimed" {
							acclaimed++
						}
						if strings.TrimSpace(r.Body) == "" {
							return fmt.Errorf(
								"review body for %q must not be empty",
								r.StoryID,
							)
						}
					}
					if len(stories) > 1 && acclaimed > 1 {
						return fmt.Errorf(
							"at most one story per issue may receive Acclaimed",
						)
					}
					return nil
				})
			if err != nil {
				return fmt.Errorf("critic review by %s: %w", critic.Name, err)
			}
			bundles[idx] = bundle
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	for i, critic := range pendingPanel {
		for _, r := range bundles[i].Reviews {
			st, ok := storyByID[r.StoryID]
			if !ok || storyHasReviewBy(*st, critic.ID) {
				continue
			}
			st.Reviews = append(st.Reviews, model.Review{
				CriticID:   critic.ID,
				CriticName: critic.Name,
				CriticRole: critic.Genre,
				Rank:       r.Rank,
				Verdict:    r.Verdict,
				Body:       strings.TrimSpace(r.Body),
				CreatedAt:  now,
			})
		}
	}

	for _, st := range stories {
		if err := e.store.SaveStory(ctx, st); err != nil {
			return err
		}
		author := aMap[st.AuthorID]
		var verdicts []string
		rankSum := 0
		for _, rev := range st.Reviews {
			verdicts = append(verdicts, rev.Verdict)
			rankSum += rev.Rank
		}
		avgRank := 0.0
		if len(st.Reviews) > 0 {
			avgRank = float64(rankSum) / float64(len(st.Reviews))
		}
		upsertStanding(&author, model.StandingEntry{
			IssueNumber: state.CurrentIssue,
			StoryID:     st.ID,
			StoryTitle:  st.Title,
			Verdicts:    verdicts,
			AverageRank: avgRank,
		})
		aMap[author.ID] = author
	}

	for _, a := range aMap {
		if a.Role == model.RoleWriter && a.Status == model.StatusActive {
			if err := e.store.SaveAgent(ctx, a); err != nil {
				return err
			}
		}
	}

	issue, err := e.store.GetIssue(ctx, state.CurrentIssue)
	if err != nil {
		return fmt.Errorf("load issue %d: %w", state.CurrentIssue, err)
	}
	issue.Status = "reviewed"
	if err := e.store.SaveIssue(ctx, issue); err != nil {
		return fmt.Errorf("save issue %d status: %w", state.CurrentIssue, err)
	}
	state.NextPhase = model.PhaseReflect
	return nil
}

type relUpdateArgs struct {
	TargetID     string `json:"target_id"     jsonschema:"Exact agent ID of the colleague"`
	TrustDelta   int    `json:"trust_delta"   jsonschema:"Integer from -2 to +2 reflecting change in trust"`
	RespectDelta int    `json:"respect_delta" jsonschema:"Integer from -2 to +2 reflecting change in literary respect"`
	Impression   string `json:"impression"    jsonschema:"1-2 sentences updating your private view of this colleague"`
}

type updateNotebookArgs struct {
	Notebook            string          `json:"notebook"                       jsonschema:"Updated 140-220 word private working notebook in first person"`
	RelationshipUpdates []relUpdateArgs `json:"relationship_updates,omitempty" jsonschema:"Optional relationship updates toward colleagues from this issue (e.g., author you edited, peers who commented on your work)"`
}

func (e *Engine) runReflect(
	ctx context.Context,
	state *model.State,
	issuesPerOpenCall int,
) error {
	skill, err := e.mustSkill("reflect-notebook")
	if err != nil {
		return err
	}
	stories, err := e.store.ListStoriesByIssue(ctx, state.CurrentIssue)
	if err != nil {
		return err
	}
	allAgents, err := e.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	writers := filterAgents(allAgents, model.RoleWriter, model.StatusActive)
	validWriterIDs := map[string]bool{}
	for _, w := range writers {
		validWriterIDs[w.ID] = true
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(2)
	for _, w := range writers {
		writer := w
		digest := buildReflectionDigest(writer, stories)
		g.Go(func() error {
			log.Printf("[reflect] %s updating notebook", writer.Name)
			res, err := adkrun.RunAction(gctx, e.adk, adkrun.Request{
				AgentName: writer.ID + "_reflect",
				Instruction: prompts.AgentInstruction(
					state.Charter,
					writer,
					skill,
				),
				UserMessage: prompts.ReflectionInput(
					state.CurrentIssue,
					digest,
				),
				Temperature: 0.5,
			}, "update_notebook", "Save your updated 140-220 word private working notebook and optional peer relationship updates.",
				func(a updateNotebookArgs) error {
					if strings.TrimSpace(a.Notebook) == "" {
						return fmt.Errorf("notebook must not be empty")
					}
					return nil
				})
			if err != nil {
				return fmt.Errorf("reflect %s: %w", writer.Name, err)
			}
			writer.Notebook = strings.TrimSpace(res.Notebook)
			for _, u := range res.RelationshipUpdates {
				if validWriterIDs[u.TargetID] && u.TargetID != writer.ID {
					applyRelationshipDelta(
						&writer,
						u.TargetID,
						u.TrustDelta,
						u.RespectDelta,
						u.Impression,
					)
				}
			}
			return e.store.SaveAgent(gctx, writer)
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	if issuesPerOpenCall > 0 && state.CurrentIssue%issuesPerOpenCall == 0 {
		state.NextPhase = model.PhaseOpenCall
	} else {
		state.NextPhase = model.PhaseCommission
	}
	return nil
}

func storyHasReviewBy(st model.Story, criticID string) bool {
	for _, r := range st.Reviews {
		if r.CriticID == criticID {
			return true
		}
	}
	return false
}

func upsertStanding(a *model.Agent, entry model.StandingEntry) {
	for i, existing := range a.Standing {
		if existing.StoryID == entry.StoryID {
			a.Standing[i] = entry
			return
		}
	}
	a.Standing = append(a.Standing, entry)
	if len(a.Standing) > 5 {
		a.Standing = a.Standing[len(a.Standing)-5:]
	}
}

func buildReflectionDigest(w model.Agent, stories []model.Story) string {
	var b strings.Builder
	for _, st := range stories {
		if st.AuthorID == w.ID {
			fmt.Fprintf(
				&b,
				"YOU PUBLISHED \"%s\" (edited by %s [id=%s]).\n",
				st.Title,
				st.EditorName,
				st.EditorID,
			)
			for _, rev := range st.Reviews {
				fmt.Fprintf(
					&b,
					"- Critic %s ranked it #%d [%s]: %s\n",
					rev.CriticName,
					rev.Rank,
					rev.Verdict,
					rev.Body,
				)
			}
			for _, c := range st.SalonComments {
				fmt.Fprintf(
					&b,
					"- Peer %s [id=%s] commented: %s\n",
					c.AuthorName,
					c.AuthorID,
					c.Body,
				)
			}
			b.WriteString("\n")
		} else {
			roleNote := "Peer published"
			if st.EditorID == w.ID {
				roleNote = "You edited"
			}
			var verdicts []string
			for _, rev := range st.Reviews {
				verdicts = append(verdicts, fmt.Sprintf("%s: %s (#%d)", rev.CriticName, rev.Verdict, rev.Rank))
			}
			fmt.Fprintf(&b, "%s \"%s\" by %s [id=%s] (%s). Critical reception: %s.\n",
				roleNote, st.Title, st.AuthorName, st.AuthorID, st.Genre, strings.Join(verdicts, "; "))
			if st.EditorID == w.ID && st.AuthorRevisionNote != "" {
				fmt.Fprintf(&b, "  Author's response to your workshop notes: %s\n", st.AuthorRevisionNote)
			}
		}
	}
	return b.String()
}

// selectBalancedCritics picks n critics semi-randomly while keeping review counts closely balanced.
func selectBalancedCritics(
	critics []model.Agent,
	counts map[string]int,
	n int,
) []model.Agent {
	if len(critics) <= n {
		return append([]model.Agent(nil), critics...)
	}
	type scored struct {
		agent model.Agent
		key   int
		tie   int
	}
	pool := make([]scored, len(critics))
	for i, c := range critics {
		pool[i] = scored{
			agent: c,
			key:   counts[c.ID] + rand.IntN(2),
			tie:   rand.IntN(1_000_000),
		}
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].key != pool[j].key {
			return pool[i].key < pool[j].key
		}
		return pool[i].tie < pool[j].tie
	})
	out := make([]model.Agent, n)
	for i := 0; i < n; i++ {
		out[i] = pool[i].agent
	}
	return out
}
