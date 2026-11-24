package adkrun

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"cornice/internal/model"
	"cornice/internal/store"
)

const (
	maxRegisterStories     = 20
	maxRegisterProceedings = 6
)

// ArchiveSnapshot caches published stories and open calls once per phase so every agent
// shares a single Firestore scan rather than re-reading the entire collection.
type ArchiveSnapshot struct {
	storyIndex map[string]model.Story
	callIndex  map[string]model.OpenCall
	stories    []model.Story
	calls      []model.OpenCall
}

// LoadArchiveSnapshot fetches published stories and past proceedings once per phase.
func LoadArchiveSnapshot(
	ctx context.Context,
	st store.Store,
) (*ArchiveSnapshot, error) {
	stories, err := st.ListStories(ctx)
	if err != nil {
		return nil, fmt.Errorf("list stories for archive snapshot: %w", err)
	}
	calls, err := st.ListOpenCalls(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open calls for archive snapshot: %w", err)
	}

	snap := &ArchiveSnapshot{
		storyIndex: make(map[string]model.Story, len(stories)),
		callIndex:  make(map[string]model.OpenCall, len(calls)),
		calls:      calls,
	}
	for _, s := range stories {
		if s.Status != "published" {
			continue
		}
		snap.storyIndex[s.ID] = s
		snap.stories = append(snap.stories, s)
	}
	for _, oc := range calls {
		snap.callIndex[oc.ID] = oc
	}
	return snap, nil
}

// ArchiveBundle bundles read-only ADK tools, a bounded Archive Register text, and a per-agent read budget.
type ArchiveBundle struct {
	Register  string
	Tools     []tool.Tool
	Guard     llmagent.BeforeToolCallback
	mu        sync.Mutex
	reads     int
	consulted []string
}

// Consulted returns the archive items the agent actually read during its final successful attempt.
func (b *ArchiveBundle) Consulted() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.consulted...)
}

// Reset clears the read counter and consulted log between RunAction retry attempts.
func (b *ArchiveBundle) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reads = 0
	b.consulted = nil
}

type storyIDArgs struct {
	StoryID string `json:"story_id" jsonschema:"Exact story_id from the Cornice Archive Register"`
}

type callIDArgs struct {
	CallID string `json:"call_id" jsonschema:"Exact call_id from the Cornice Archive Register"`
}

type textResult struct {
	Content string `json:"content"`
}

// NewSession builds a per-agent ArchiveBundle backed by the shared ArchiveSnapshot.
func (snap *ArchiveSnapshot) NewSession(
	agentLabel string,
	maxReads int,
) (*ArchiveBundle, error) {
	var reg strings.Builder
	fmt.Fprintf(
		&reg,
		"CORNICE ARCHIVE REGISTER (optional read_reviews, read_story, or read_proceeding — max %d reads):\n",
		maxReads,
	)

	shownStories := 0
	for _, s := range snap.stories {
		if shownStories >= maxRegisterStories {
			fmt.Fprintf(
				&reg,
				"- ... (%d older stories omitted from register)\n",
				len(snap.stories)-shownStories,
			)
			break
		}
		shownStories++
		scoreStr := "unreviewed"
		if len(s.Reviews) > 0 {
			scoreStr = fmt.Sprintf(
				"%.1f/4 (%d reviews)",
				avgStoryScore(s.Reviews),
				len(s.Reviews),
			)
		}
		fmt.Fprintf(
			&reg,
			"- [story] id=%s | Issue %d | \"%s\" by %s (%s) | %s\n",
			s.ID,
			s.IssueNumber,
			s.Title,
			s.AuthorName,
			s.Genre,
			scoreStr,
		)
		if s.Premise != "" {
			fmt.Fprintf(&reg, "    Premise: %s\n", s.Premise)
		}
	}
	if shownStories == 0 {
		reg.WriteString("- (No published stories in the archive yet.)\n")
	}

	shownCalls := 0
	for _, oc := range snap.calls {
		if shownCalls >= maxRegisterProceedings {
			break
		}
		shownCalls++
		fmt.Fprintf(
			&reg,
			"- [proceeding] id=%s | After Issue %d | %s\n",
			oc.ID,
			oc.AfterIssue,
			oc.Summary,
		)
	}

	bundle := &ArchiveBundle{
		Register: reg.String(),
	}

	recordRead := func(toolName, targetID string) {
		bundle.mu.Lock()
		defer bundle.mu.Unlock()
		bundle.reads++
		entry := fmt.Sprintf("%s(%s)", toolName, targetID)
		bundle.consulted = append(bundle.consulted, entry)
		log.Printf(
			"  [archive %d/%d] %s -> %s",
			bundle.reads,
			maxReads,
			agentLabel,
			entry,
		)
	}

	readReviewsTool, err := functiontool.New(
		functiontool.Config{
			Name:        "read_reviews",
			Description: "Read all critic reviews and peer salon comments for a published Cornice story.",
		},
		func(_ agent.Context, args storyIDArgs) (textResult, error) {
			id := strings.TrimSpace(args.StoryID)
			s, ok := snap.storyIndex[id]
			if !ok {
				return textResult{
					Content: fmt.Sprintf(
						"No published story found with id %q (this lookup did not count against your read budget).",
						id,
					),
				}, nil
			}
			recordRead("read_reviews", id)
			var out strings.Builder
			fmt.Fprintf(
				&out,
				"REVIEWS & SALON FOR \"%s\" by %s (Issue %d):\n\n",
				s.Title,
				s.AuthorName,
				s.IssueNumber,
			)
			if len(s.Reviews) == 0 {
				out.WriteString("(No critic reviews yet.)\n")
			}
			for _, r := range s.Reviews {
				fmt.Fprintf(
					&out,
					"Critic %s — %s:\n%s\n\n",
					r.CriticName,
					r.Verdict,
					r.Body,
				)
			}
			if len(s.SalonComments) > 0 {
				out.WriteString("PEER SALON:\n")
				for _, c := range s.SalonComments {
					fmt.Fprintf(&out, "- %s: %s\n", c.AuthorName, c.Body)
				}
			}
			return textResult{Content: out.String()}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	readStoryTool, err := functiontool.New(
		functiontool.Config{
			Name:        "read_story",
			Description: "Read the full published prose of a Cornice story from the Archive Register.",
		},
		func(_ agent.Context, args storyIDArgs) (textResult, error) {
			id := strings.TrimSpace(args.StoryID)
			s, ok := snap.storyIndex[id]
			if !ok {
				return textResult{
					Content: fmt.Sprintf(
						"No published story found with id %q (this lookup did not count against your read budget).",
						id,
					),
				}, nil
			}
			recordRead("read_story", id)
			return textResult{
				Content: fmt.Sprintf("\"%s\" by %s (%s, Issue %d)\n\n%s",
					s.Title, s.AuthorName, s.Genre, s.IssueNumber, s.FinalText),
			}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	readProceedingTool, err := functiontool.New(
		functiontool.Config{
			Name:        "read_proceeding",
			Description: "Read a past Cornice Open Call's summary, applicants, ballots, and retention votes.",
		},
		func(_ agent.Context, args callIDArgs) (textResult, error) {
			id := strings.TrimSpace(args.CallID)
			oc, ok := snap.callIndex[id]
			if !ok {
				return textResult{
					Content: fmt.Sprintf(
						"No proceeding found with id %q (this lookup did not count against your read budget).",
						id,
					),
				}, nil
			}
			recordRead("read_proceeding", id)
			var out strings.Builder
			fmt.Fprintf(
				&out,
				"OPEN CALL AFTER ISSUE %d\nSummary: %s\n\nApplicants:\n",
				oc.AfterIssue,
				oc.Summary,
			)
			for _, a := range oc.Applicants {
				fmt.Fprintf(
					&out,
					"- %s (%s): \"%s\"\n",
					a.Name,
					a.Genre,
					a.SampleTitle,
				)
			}
			out.WriteString("\nBallots:\n")
			for _, b := range oc.Ballots {
				fmt.Fprintf(
					&out,
					"- %s voted admit=%s (eject=%s): %s\n",
					b.VoterName,
					b.AdmitID,
					b.EjectID,
					b.AdmitReason,
				)
			}
			for _, m := range oc.RetentionMotions {
				fmt.Fprintf(
					&out,
					"\nRetention Vote (%s): %s\n",
					m.TargetName,
					m.Summary,
				)
			}
			return textResult{Content: out.String()}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	bundle.Tools = []tool.Tool{
		readReviewsTool,
		readStoryTool,
		readProceedingTool,
	}
	bundle.Guard = func(_ agent.Context, t tool.Tool, _ map[string]any) (map[string]any, error) {
		name := t.Name()
		if name != "read_reviews" && name != "read_story" &&
			name != "read_proceeding" {
			return nil, nil
		}
		bundle.mu.Lock()
		over := bundle.reads >= maxReads
		bundle.mu.Unlock()
		if over {
			return map[string]any{
				"content": fmt.Sprintf(
					"Archive read limit (%d) reached. Proceed directly to your submission tool.",
					maxReads,
				),
			}, nil
		}
		return nil, nil
	}

	return bundle, nil
}

func avgStoryScore(reviews []model.Review) float64 {
	pts := map[string]float64{
		"Acclaimed": 4,
		"Commended": 3,
		"Mixed":     2,
		"Panned":    1,
	}
	var sum float64
	for _, r := range reviews {
		sum += pts[r.Verdict]
	}
	if len(reviews) == 0 {
		return 0
	}
	return sum / float64(len(reviews))
}
