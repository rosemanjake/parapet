package prompts

import (
	"fmt"
	"strings"

	"cornice/internal/model"
)

// AgentInstruction composes the Charter, the agent's persona & memory, and the active SKILL.md.
func AgentInstruction(charter string, a model.Agent, skillBody string) string {
	var b strings.Builder
	b.WriteString(charter)
	b.WriteString("\n\n---\n\n")
	fmt.Fprintf(&b, "YOU ARE: %s\n", a.Name)
	if a.Role == model.RoleCritic {
		fmt.Fprintf(
			&b,
			"ROLE: Permanent Literary Critic (%s) at Cornice.\n",
			a.Genre,
		)
	} else {
		fmt.Fprintf(&b, "ROLE: Member Writer of the Cornice Literary Collective.\n")
		fmt.Fprintf(&b, "PRIMARY GENRE / BEAT: %s\n", a.Genre)
	}
	fmt.Fprintf(&b, "BACKGROUND: %s\n", a.Bio)
	fmt.Fprintf(&b, "VOICE & CRAFT: %s\n", a.VoiceCraft)
	fmt.Fprintf(&b, "WHAT YOU REFUSE TO WRITE / DESPISE: %s\n", a.NegativeRules)
	fmt.Fprintf(&b, "EDITORIAL TEMPERAMENT: %s\n", a.EditorialTemperament)

	if strings.TrimSpace(a.Notebook) != "" {
		fmt.Fprintf(
			&b,
			"\nYOUR WORKING NOTEBOOK (PRIVATE MEMORY):\n%s\n",
			a.Notebook,
		)
	}
	if len(a.Standing) > 0 {
		b.WriteString("\nYOUR RECENT CRITICAL STANDING:\n")
		for _, s := range a.Standing {
			fmt.Fprintf(
				&b,
				"- Issue %d \"%s\": verdicts [%s], avg rank %.1f\n",
				s.IssueNumber,
				s.StoryTitle,
				strings.Join(s.Verdicts, ", "),
				s.AverageRank,
			)
		}
	}
	b.WriteString(
		"\nStay completely in character. Never break the fourth wall or mention being an AI.",
	)
	if strings.TrimSpace(skillBody) != "" {
		b.WriteString("\n\n---\n\n")
		b.WriteString(skillBody)
	}
	return b.String()
}

// CharterInstruction composes the Charter with a standalone SKILL.md (for non-persona tasks like applicant generation).
func CharterInstruction(charter, skillBody string) string {
	return charter + "\n\n---\n\n" + skillBody
}

// CommissionInput supplies the issue number and Archive Register for commissioning a new story.
func CommissionInput(issueNumber int, archiveRegister string) string {
	return fmt.Sprintf(
		"Commission for Cornice Issue %d.\n\n%s\n\nBrowse up to 3 items from the register if useful (or none), then return your commissioned title, premise, and outline.",
		issueNumber,
		archiveRegister,
	)
}

// DraftInput supplies the commissioned title, premise, and beat outline for drafting.
func DraftInput(st model.Story) string {
	return fmt.Sprintf(
		"Cornice Issue %d — First Draft\n\nTITLE: %s\nPREMISE: %s\nOUTLINE:\n%s",
		st.IssueNumber,
		st.Title,
		st.Premise,
		st.Outline,
	)
}

// ChooseEditorInput lists eligible peers and the author's relationship impressions.
func ChooseEditorInput(
	author model.Agent,
	st model.Story,
	peers []model.Agent,
) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"Story: \"%s\" (%s)\n\nAVAILABLE PEERS:\n",
		st.Title,
		st.Premise,
	)
	for _, p := range peers {
		rel := author.Relationships[p.ID]
		fmt.Fprintf(
			&b,
			"- ID: %s | Name: %s | Genre: %s | Temperament: %s\n",
			p.ID,
			p.Name,
			p.Genre,
			p.EditorialTemperament,
		)
		fmt.Fprintf(
			&b,
			"  Your current view (trust %+d, respect %+d): %s\n",
			rel.Trust,
			rel.Respect,
			emptyFallback(rel.Impression, "No strong impression yet."),
		)
	}
	return b.String()
}

// WorkshopCritiqueInput supplies the draft and relationship context to the selected peer editor.
func WorkshopCritiqueInput(editor, author model.Agent, st model.Story) string {
	rel := editor.Relationships[author.ID]
	return fmt.Sprintf(
		"Author: %s (%s) — Cornice Issue %d\nTitle: \"%s\"\nYour view of %s (trust %+d, respect %+d): %s\n\nFIRST DRAFT:\n---\n%s\n---",
		author.Name,
		st.Genre,
		st.IssueNumber,
		st.Title,
		author.Name,
		rel.Trust,
		rel.Respect,
		emptyFallback(rel.Impression, "Colleague in the collective."),
		st.DraftV1,
	)
}

// ReviseStoryInput supplies the peer editor's notes and the first draft for full prose revision.
func ReviseStoryInput(editor model.Agent, st model.Story) string {
	return fmt.Sprintf(
		"Cornice Issue %d — Final Revision of \"%s\"\n\nWORKSHOP NOTES FROM %s:\n---\n%s\n---\n\nYOUR FIRST DRAFT:\n---\n%s\n---",
		st.IssueNumber,
		st.Title,
		editor.Name,
		st.EditorNotes,
		st.DraftV1,
	)
}

// ReviseMetadataInput supplies the workshop notes and final revised prose to generate metadata and relationship deltas.
func ReviseMetadataInput(author, editor model.Agent, st model.Story) string {
	rel := author.Relationships[editor.ID]
	return fmt.Sprintf(
		"Working title: \"%s\"\nEditor: %s (prior view: trust %+d, respect %+d — %s)\n\nWORKSHOP NOTES FROM %s:\n---\n%s\n---\n\nFINAL REVISED STORY:\n---\n%s\n---",
		st.Title,
		editor.Name,
		rel.Trust,
		rel.Respect,
		emptyFallback(rel.Impression, "Colleague."),
		editor.Name,
		st.EditorNotes,
		st.FinalText,
	)
}

// SalonCommentInput supplies a published story and the reader's prior view of the author.
func SalonCommentInput(reader model.Agent, st model.Story) string {
	rel := reader.Relationships[st.AuthorID]
	return fmt.Sprintf(
		"Cornice Issue %d — \"%s\" by %s (%s)\nYour view of %s (trust %+d, respect %+d): %s\n\nPUBLISHED STORY:\n---\n%s\n---",
		st.IssueNumber,
		st.Title,
		st.AuthorName,
		st.Genre,
		st.AuthorName,
		rel.Trust,
		rel.Respect,
		emptyFallback(rel.Impression, "Colleague."),
		st.FinalText,
	)
}

// CriticIssueReviewInput supplies all stories in the issue plus the optional Archive Register.
func CriticIssueReviewInput(
	issueNumber int,
	stories []model.Story,
	archiveRegister string,
) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"Assigned review packet for Cornice Issue %d (%d piece(s)).\n\n%s\n\n",
		issueNumber,
		len(stories),
		archiveRegister,
	)
	for i, st := range stories {
		fmt.Fprintf(
			&b,
			"=== STORY %d ===\nID: %s\nTITLE: %s\nAUTHOR: %s (%s)\n\n%s\n\n",
			i+1,
			st.ID,
			st.Title,
			st.AuthorName,
			st.Genre,
			st.FinalText,
		)
	}
	return b.String()
}

// ReflectionInput supplies the digest of what happened in the just-reviewed Issue.
func ReflectionInput(issueNumber int, digest string) string {
	return fmt.Sprintf(
		"Cornice Issue %d summary relevant to you:\n\n%s",
		issueNumber,
		digest,
	)
}

// GenerateApplicantInput supplies current members (to avoid duplication) and recent issue summaries.
func GenerateApplicantInput(
	activeWriters []model.Agent,
	recentIssuesSummary string,
	slotIndex int,
) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"Applicant Slot #%d\n\nCURRENT COLLECTIVE MEMBERS (do not duplicate their genre, name, or voice):\n",
		slotIndex,
	)
	for _, w := range activeWriters {
		fmt.Fprintf(&b, "- %s (%s): %s\n", w.Name, w.Genre, w.Bio)
	}
	if recentIssuesSummary != "" {
		fmt.Fprintf(&b, "\nRECENT CORNICE ISSUES:\n%s\n", recentIssuesSummary)
	}
	return b.String()
}

// ReturningAlumnusInput supplies the current roster and issues published since the alumnus departed.
func ReturningAlumnusInput(
	alumnus model.Agent,
	activeWriters []model.Agent,
	recentIssuesSummary string,
) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"You were ejected after Issue %d.\n\nCURRENT COLLECTIVE MEMBERS:\n",
		alumnus.EjectedIssue,
	)
	for _, w := range activeWriters {
		fmt.Fprintf(&b, "- %s (%s)\n", w.Name, w.Genre)
	}
	if recentIssuesSummary != "" {
		fmt.Fprintf(
			&b,
			"\nRECENT ISSUES SINCE YOU LEFT:\n%s\n",
			recentIssuesSummary,
		)
	}
	return b.String()
}

// SampleStoryInput supplies the applicant's proposed title and premise for writing their sample piece.
func SampleStoryInput(title, premise string) string {
	return fmt.Sprintf("TITLE: %s\nPREMISE: %s", title, premise)
}

// OpenCallVoteInput supplies the capacity state, roster standings, applicants, and Archive Register.
func OpenCallVoteInput(
	voter model.Agent,
	activeWriters []model.Agent,
	applicants []model.Applicant,
	maxMembers int,
	archiveRegister string,
) string {
	var b strings.Builder
	atCap := len(activeWriters) >= maxMembers
	fmt.Fprintf(
		&b,
		"Seats filled: %d / %d. At capacity: %v.\n\n%s\n\n=== CURRENT COLLECTIVE ROSTER & STANDING ===\n",
		len(activeWriters),
		maxMembers,
		atCap,
		archiveRegister,
	)
	for _, w := range activeWriters {
		selfTag := ""
		if w.ID == voter.ID {
			selfTag = " [YOU — cannot eject]"
		}
		rel := voter.Relationships[w.ID]
		fmt.Fprintf(
			&b,
			"- ID: %s | %s (%s)%s\n",
			w.ID,
			w.Name,
			w.Genre,
			selfTag,
		)
		if len(w.Standing) == 0 {
			b.WriteString("  Recent standing: no reviewed stories yet.\n")
		} else {
			var parts []string
			for _, s := range w.Standing {
				parts = append(parts, fmt.Sprintf("Issue %d \"%s\" [%s, avg rank %.1f]", s.IssueNumber, s.StoryTitle, strings.Join(s.Verdicts, "/"), s.AverageRank))
			}
			fmt.Fprintf(&b, "  Recent standing: %s\n", strings.Join(parts, "; "))
		}
		if w.ID != voter.ID {
			fmt.Fprintf(
				&b,
				"  Your view (trust %+d, respect %+d): %s\n",
				rel.Trust,
				rel.Respect,
				emptyFallback(rel.Impression, "Colleague."),
			)
		}
	}

	b.WriteString("\n=== APPLICANTS ===\n")
	for _, app := range applicants {
		retTag := ""
		if app.IsReturningAlumni {
			retTag = " [RETURNING ALUMNUS]"
		}
		fmt.Fprintf(
			&b,
			"--- APPLICANT ID: %s ---\nName: %s | Proposed Genre: %s%s\nBio: %s\n\nCover Letter:\n%s\n\nSample Story — \"%s\":\n%s\n\n",
			app.ID,
			app.Name,
			app.Genre,
			retTag,
			app.Bio,
			app.CoverLetter,
			app.SampleTitle,
			app.SampleStory,
		)
	}
	return b.String()
}

// RetentionVoteInput supplies the flagged member's critical record, peer view, and Archive Register.
func RetentionVoteInput(
	voter, target model.Agent,
	triggerReason, archiveRegister string,
) string {
	var b strings.Builder
	rel := voter.Relationships[target.ID]
	fmt.Fprintf(
		&b,
		"Subject: %s (%s)\nTrigger: %s\n\n%s\n\nRECENT CRITICAL RECORD:\n",
		target.Name,
		target.Genre,
		triggerReason,
		archiveRegister,
	)
	for _, s := range target.Standing {
		fmt.Fprintf(
			&b,
			"- Issue %d \"%s\": verdicts [%s], avg rank %.1f\n",
			s.IssueNumber,
			s.StoryTitle,
			strings.Join(s.Verdicts, ", "),
			s.AverageRank,
		)
	}
	fmt.Fprintf(
		&b,
		"\nYour private view of %s (trust %+d, respect %+d): %s",
		target.Name,
		rel.Trust,
		rel.Respect,
		emptyFallback(rel.Impression, "Colleague."),
	)
	return b.String()
}

func emptyFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
