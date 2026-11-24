package model

import "time"

// Phase represents one discrete step in Cornice's publishing cadence.
type Phase string

const (
	PhaseCommission Phase = "commission"
	PhaseDraft      Phase = "draft"
	PhaseWorkshop   Phase = "workshop"
	PhasePublish    Phase = "publish"
	PhaseReview     Phase = "review"
	PhaseReflect    Phase = "reflect"
	PhaseOpenCall   Phase = "open_call"
)

// Role distinguishes collective writers from permanent critics.
type Role string

const (
	RoleWriter Role = "writer"
	RoleCritic Role = "critic"
)

// AgentStatus tracks whether a writer is active or ejected (alumni).
type AgentStatus string

const (
	StatusActive  AgentStatus = "active"
	StatusEjected AgentStatus = "ejected"
)

// State is the singleton document stored at meta/state.
type State struct {
	CurrentIssue      int        `firestore:"current_issue"        json:"current_issue"`
	NextPhase         Phase      `firestore:"next_phase"           json:"next_phase"`
	AuthorQueue       []string   `firestore:"author_queue"         json:"author_queue"`
	MaxMembers        int        `firestore:"max_members"          json:"max_members"`
	StoriesPerIssue   int        `firestore:"stories_per_issue"    json:"stories_per_issue"`
	IssuesPerOpenCall int        `firestore:"issues_per_open_call" json:"issues_per_open_call"`
	Charter           string     `firestore:"charter"              json:"charter"`
	TokenUsage        TokenUsage `firestore:"token_usage"          json:"token_usage"`
	LastTickAt        time.Time  `firestore:"last_tick_at"         json:"last_tick_at"`
	LockOwner         string     `firestore:"lock_owner,omitempty" json:"lock_owner,omitempty"`
	LockUntil         time.Time  `firestore:"lock_until,omitempty" json:"lock_until,omitempty"`
}

// TokenUsage accumulates Gemini token spend across ticks.
type TokenUsage struct {
	InputTokens   int64 `firestore:"input_tokens"   json:"input_tokens"`
	OutputTokens  int64 `firestore:"output_tokens"  json:"output_tokens"`
	ThoughtTokens int64 `firestore:"thought_tokens" json:"thought_tokens"`
	Calls         int64 `firestore:"calls"          json:"calls"`
}

// Relationship captures a directed opinion from one agent toward another.
type Relationship struct {
	Trust      int    `firestore:"trust"      json:"trust"`      // -5 to +5
	Respect    int    `firestore:"respect"    json:"respect"`    // -5 to +5
	Impression string `firestore:"impression" json:"impression"` // 1-2 sentences
}

// StandingEntry records critical reception for one published piece.
type StandingEntry struct {
	IssueNumber int      `firestore:"issue_number" json:"issue_number"`
	StoryID     string   `firestore:"story_id"     json:"story_id"`
	StoryTitle  string   `firestore:"story_title"  json:"story_title"`
	Verdicts    []string `firestore:"verdicts"     json:"verdicts"`
	AverageRank float64  `firestore:"average_rank" json:"average_rank"`
}

// Agent represents a writer in the collective or a permanent critic.
type Agent struct {
	ID                   string                  `firestore:"id"                                json:"id"`
	Name                 string                  `firestore:"name"                              json:"name"`
	Role                 Role                    `firestore:"role"                              json:"role"`
	Status               AgentStatus             `firestore:"status"                            json:"status"`
	Genre                string                  `firestore:"genre"                             json:"genre"`
	Bio                  string                  `firestore:"bio"                               json:"bio"`
	VoiceCraft           string                  `firestore:"voice_craft"                       json:"voice_craft"`
	NegativeRules        string                  `firestore:"negative_rules"                    json:"negative_rules"`
	EditorialTemperament string                  `firestore:"editorial_temperament"             json:"editorial_temperament"`
	Notebook             string                  `firestore:"notebook"                          json:"notebook"`
	Standing             []StandingEntry         `firestore:"standing"                          json:"standing"`
	Relationships        map[string]Relationship `firestore:"relationships"                     json:"relationships"`
	IsFounder            bool                    `firestore:"is_founder"                        json:"is_founder"`
	JoinedIssue          int                     `firestore:"joined_issue"                      json:"joined_issue"`
	EjectedIssue         int                     `firestore:"ejected_issue"                     json:"ejected_issue"`
	LastAppliedIssue     int                     `firestore:"last_applied_issue,omitempty"      json:"last_applied_issue,omitempty"`
	LastRetentionStoryID string                  `firestore:"last_retention_story_id,omitempty" json:"last_retention_story_id,omitempty"`
	CreatedAt            time.Time               `firestore:"created_at"                        json:"created_at"`
}

// Issue groups the stories published in one issue of Cornice.
type Issue struct {
	Number      int            `firestore:"number"       json:"number"`
	Status      string         `firestore:"status"       json:"status"` // commissioning, drafting, workshop, published, reviewed
	PublishedAt time.Time      `firestore:"published_at" json:"published_at"`
	Stories     []StorySummary `firestore:"stories"      json:"stories"`
}

// StorySummary is embedded in Issue for single-read table-of-contents rendering.
type StorySummary struct {
	ID         string `firestore:"id"                  json:"id"`
	Slug       string `firestore:"slug"                json:"slug"`
	Title      string `firestore:"title"               json:"title"`
	AuthorID   string `firestore:"author_id"           json:"author_id"`
	AuthorName string `firestore:"author_name"         json:"author_name"`
	Genre      string `firestore:"genre"               json:"genre"`
	Synopsis   string `firestore:"synopsis,omitempty"  json:"synopsis,omitempty"`
	Epigraph   string `firestore:"epigraph,omitempty"  json:"epigraph,omitempty"`
	ImageURL   string `firestore:"image_url,omitempty" json:"image_url,omitempty"`
}

// Story stores a full piece along with its workshop history, reviews, and salon notes.
type Story struct {
	ID                 string         `firestore:"id"                     json:"id"`
	Slug               string         `firestore:"slug"                   json:"slug"`
	IssueNumber        int            `firestore:"issue_number"           json:"issue_number"`
	Status             string         `firestore:"status"                 json:"status"` // commissioned, drafted, workshopped, published
	AuthorID           string         `firestore:"author_id"              json:"author_id"`
	AuthorName         string         `firestore:"author_name"            json:"author_name"`
	Genre              string         `firestore:"genre"                  json:"genre"`
	Title              string         `firestore:"title"                  json:"title"`
	Premise            string         `firestore:"premise"                json:"premise"`
	Outline            string         `firestore:"outline"                json:"outline"`
	Consulted          []string       `firestore:"consulted,omitempty"    json:"consulted,omitempty"`
	Synopsis           string         `firestore:"synopsis,omitempty"     json:"synopsis,omitempty"`
	Epigraph           string         `firestore:"epigraph,omitempty"     json:"epigraph,omitempty"`
	ImagePrompt        string         `firestore:"image_prompt,omitempty" json:"image_prompt,omitempty"`
	ImageURL           string         `firestore:"image_url,omitempty"    json:"image_url,omitempty"`
	DraftV1            string         `firestore:"draft_v1"               json:"draft_v1"`
	EditorID           string         `firestore:"editor_id"              json:"editor_id"`
	EditorName         string         `firestore:"editor_name"            json:"editor_name"`
	EditorSelectReason string         `firestore:"editor_select_reason"   json:"editor_select_reason"`
	EditorNotes        string         `firestore:"editor_notes"           json:"editor_notes"`
	AuthorRevisionNote string         `firestore:"author_revision_note"   json:"author_revision_note"`
	FinalText          string         `firestore:"final_text"             json:"final_text"`
	Reviews            []Review       `firestore:"reviews"                json:"reviews"`
	SalonComments      []SalonComment `firestore:"salon_comments"         json:"salon_comments"`
	CreatedAt          time.Time      `firestore:"created_at"             json:"created_at"`
	PublishedAt        time.Time      `firestore:"published_at"           json:"published_at"`
}

// Review is a signed critic review attached to a published story.
type Review struct {
	CriticID   string    `firestore:"critic_id"   json:"critic_id"`
	CriticName string    `firestore:"critic_name" json:"critic_name"`
	CriticRole string    `firestore:"critic_role" json:"critic_role"`
	Rank       int       `firestore:"rank"        json:"rank"`
	Verdict    string    `firestore:"verdict"     json:"verdict"` // Acclaimed, Commended, Mixed, Panned
	Body       string    `firestore:"body"        json:"body"`
	CreatedAt  time.Time `firestore:"created_at"  json:"created_at"`
}

// SalonComment is a peer member's published marginalia on a story.
type SalonComment struct {
	AuthorID    string    `firestore:"author_id"    json:"author_id"`
	AuthorName  string    `firestore:"author_name"  json:"author_name"`
	AuthorGenre string    `firestore:"author_genre" json:"author_genre"`
	Body        string    `firestore:"body"         json:"body"`
	CreatedAt   time.Time `firestore:"created_at"   json:"created_at"`
}

// OpenCall records a periodic admissions, retention, and (at cap) ejection vote.
type OpenCall struct {
	ID               string            `firestore:"id"                          json:"id"`
	AfterIssue       int               `firestore:"after_issue"                 json:"after_issue"`
	Applicants       []Applicant       `firestore:"applicants"                  json:"applicants"`
	Ballots          []Ballot          `firestore:"ballots"                     json:"ballots"`
	AdmittedIDs      []string          `firestore:"admitted_ids"                json:"admitted_ids"`
	EjectedIDs       []string          `firestore:"ejected_ids"                 json:"ejected_ids"`
	RetentionMotions []RetentionMotion `firestore:"retention_motions,omitempty" json:"retention_motions,omitempty"`
	Summary          string            `firestore:"summary"                     json:"summary"`
	ResolvedAt       time.Time         `firestore:"resolved_at"                 json:"resolved_at"`
}

// RetentionMotion records a standing-triggered vote on whether to keep or eject a struggling member.
type RetentionMotion struct {
	TargetID      string            `firestore:"target_id"      json:"target_id"`
	TargetName    string            `firestore:"target_name"    json:"target_name"`
	TriggerReason string            `firestore:"trigger_reason" json:"trigger_reason"`
	Ballots       []RetentionBallot `firestore:"ballots"        json:"ballots"`
	RetainVotes   int               `firestore:"retain_votes"   json:"retain_votes"`
	EjectVotes    int               `firestore:"eject_votes"    json:"eject_votes"`
	Ejected       bool              `firestore:"ejected"        json:"ejected"`
	Summary       string            `firestore:"summary"        json:"summary"`
}

// RetentionBallot records one member's up/down vote on a RetentionMotion.
type RetentionBallot struct {
	VoterID   string `firestore:"voter_id"   json:"voter_id"`
	VoterName string `firestore:"voter_name" json:"voter_name"`
	Vote      string `firestore:"vote"       json:"vote"` // "retain" or "eject"
	Reason    string `firestore:"reason"     json:"reason"`
}

// Applicant is a candidate pitching to join (or rejoin) the collective.
type Applicant struct {
	ID                   string `firestore:"id"                    json:"id"`
	Name                 string `firestore:"name"                  json:"name"`
	Genre                string `firestore:"genre"                 json:"genre"`
	Bio                  string `firestore:"bio"                   json:"bio"`
	VoiceCraft           string `firestore:"voice_craft"           json:"voice_craft"`
	NegativeRules        string `firestore:"negative_rules"        json:"negative_rules"`
	EditorialTemperament string `firestore:"editorial_temperament" json:"editorial_temperament"`
	CoverLetter          string `firestore:"cover_letter"          json:"cover_letter"`
	SampleTitle          string `firestore:"sample_title"          json:"sample_title"`
	SampleStory          string `firestore:"sample_story"          json:"sample_story"`
	IsReturningAlumni    bool   `firestore:"is_returning_alumni"   json:"is_returning_alumni"`
}

// Ballot records one member's vote in an Open Call.
type Ballot struct {
	VoterID     string `firestore:"voter_id"     json:"voter_id"`
	VoterName   string `firestore:"voter_name"   json:"voter_name"`
	AdmitID     string `firestore:"admit_id"     json:"admit_id"` // applicant ID or "none"
	AdmitReason string `firestore:"admit_reason" json:"admit_reason"`
	EjectID     string `firestore:"eject_id"     json:"eject_id"` // member ID or ""
	EjectReason string `firestore:"eject_reason" json:"eject_reason"`
}
