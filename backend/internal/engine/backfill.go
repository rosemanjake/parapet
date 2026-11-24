package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"cornice/internal/adkrun"
	"cornice/internal/media"
	"cornice/internal/model"
	"cornice/internal/prompts"
)

type editorialPackageArgs struct {
	Synopsis   string `json:"synopsis"    jsonschema:"One plain, concrete, non-spoiler sentence (under 24 words) stating what the story is about"`
	ImageScene string `json:"image_scene" jsonschema:"One quiet, concrete visual subject or landscape moment from the story for a 35mm editorial headline photograph (no text, no faces posing)"`
}

// ensureEditorialAssets generates a missing one-sentence synopsis and/or Nano Banana headline photograph
// for a story, uploading the photograph to GCS (or local media dir).
func (e *Engine) ensureEditorialAssets(
	ctx context.Context,
	charter string,
	st *model.Story,
) error {
	if strings.TrimSpace(st.Synopsis) == "" ||
		strings.TrimSpace(st.ImagePrompt) == "" {
		skill, err := e.mustSkill("editorial-package")
		if err != nil {
			return err
		}
		text := st.FinalText
		if text == "" {
			text = st.DraftV1
		}
		log.Printf(
			"[media] generating synopsis & image scene for \"%s\"...",
			st.Title,
		)
		res, err := adkrun.RunAction(ctx, e.adk, adkrun.Request{
			AgentName:   st.ID + "_package",
			Instruction: prompts.CharterInstruction(charter, skill),
			UserMessage: fmt.Sprintf(
				"TITLE: %s\nAUTHOR: %s (%s)\n\nSTORY:\n---\n%s\n---",
				st.Title, st.AuthorName, st.Genre, text,
			),
			Temperature: 0.4,
		}, "submit_editorial_package", "Submit a one-sentence synopsis and quiet 35mm photograph scene brief.",
			func(a editorialPackageArgs) error {
				if strings.TrimSpace(a.Synopsis) == "" ||
					strings.TrimSpace(a.ImageScene) == "" {
					return fmt.Errorf(
						"both synopsis and image_scene must be non-empty",
					)
				}
				return nil
			})
		if err != nil {
			return err
		}
		if strings.TrimSpace(st.Synopsis) == "" {
			st.Synopsis = strings.TrimSpace(res.Synopsis)
		}
		if strings.TrimSpace(st.ImagePrompt) == "" {
			st.ImagePrompt = media.BuildPrompt(res.ImageScene)
		}
		log.Printf("  -> synopsis: %s", st.Synopsis)
	}

	if strings.TrimSpace(st.ImageURL) == "" {
		log.Printf(
			"[media] generating Nano Banana 35mm image for \"%s\"...",
			st.Title,
		)
		imgBytes, mimeType, err := media.GenerateStoryImage(
			ctx,
			e.cfg,
			st.ImagePrompt,
		)
		if err != nil {
			return fmt.Errorf("generate headline image for %s: %w", st.ID, err)
		}
		url, err := media.UploadStoryImage(
			ctx,
			e.cfg,
			st.Slug,
			imgBytes,
			mimeType,
		)
		if err != nil {
			return fmt.Errorf("upload headline image for %s: %w", st.ID, err)
		}
		st.ImageURL = url
		log.Printf("  -> uploaded %s (%d KB)", url, len(imgBytes)/1024)
	}
	return nil
}

// BackfillAssets generates missing synopses and Nano Banana headline images for all published stories.
func (e *Engine) BackfillAssets(ctx context.Context) error {
	state, err := e.loadOrInitState(ctx)
	if err != nil {
		return err
	}
	if charter, err := e.ed.Charter(); err == nil && charter != "" {
		state.Charter = charter
	}

	stories, err := e.store.ListStories(ctx)
	if err != nil {
		return err
	}

	startUsage := e.adk.Usage()
	updatedIssues := map[int]bool{}
	updatedCount := 0

	for _, st := range stories {
		if st.Status != "published" {
			continue
		}
		if strings.TrimSpace(st.Synopsis) != "" &&
			strings.TrimSpace(st.ImageURL) != "" {
			log.Printf(
				"[backfill] skipping \"%s\" (already has synopsis + image)",
				st.Title,
			)
			continue
		}
		if err := e.ensureEditorialAssets(ctx, state.Charter, &st); err != nil {
			return fmt.Errorf("backfill %s: %w", st.ID, err)
		}
		if err := e.store.SaveStory(ctx, st); err != nil {
			return err
		}
		updatedIssues[st.IssueNumber] = true
		updatedCount++
	}

	// Keep Issue StorySummary projections in sync.
	for issueNum := range updatedIssues {
		issue, err := e.store.GetIssue(ctx, issueNum)
		if err != nil {
			continue
		}
		issueStories, err := e.store.ListStoriesByIssue(ctx, issueNum)
		if err != nil {
			continue
		}
		var summaries []model.StorySummary
		for _, st := range issueStories {
			if st.Status != "published" {
				continue
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
		issue.Stories = summaries
		_ = e.store.SaveIssue(ctx, issue)
	}

	endUsage := e.adk.Usage()
	state.TokenUsage.Calls += endUsage.Calls - startUsage.Calls
	state.TokenUsage.InputTokens += endUsage.InputTokens - startUsage.InputTokens
	state.TokenUsage.OutputTokens += endUsage.OutputTokens - startUsage.OutputTokens
	state.TokenUsage.ThoughtTokens += endUsage.ThoughtTokens - startUsage.ThoughtTokens
	state.LastTickAt = time.Now().UTC()
	if err := e.store.SaveState(ctx, state); err != nil {
		return err
	}

	log.Printf(
		"[backfill] complete: updated %d published stories",
		updatedCount,
	)
	return nil
}
