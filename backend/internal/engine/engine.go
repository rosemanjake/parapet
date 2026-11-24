package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"cornice/editorial"
	"cornice/internal/adkrun"
	"cornice/internal/config"
	"cornice/internal/model"
	"cornice/internal/seed"
	"cornice/internal/store"
)

const tickLockTTL = 30 * time.Minute

// Engine coordinates Cornice's deterministic publishing phases using ADK Go v2 agents and Markdown skills.
type Engine struct {
	store store.Store
	adk   *adkrun.Runtime
	ed    *editorial.Loader
	cfg   config.Config
}

// New constructs an Engine. adkRuntime may be nil for read-only commands like status or seed.
func New(
	st store.Store,
	adkRuntime *adkrun.Runtime,
	ed *editorial.Loader,
	cfg config.Config,
) *Engine {
	return &Engine{
		store: st,
		adk:   adkRuntime,
		ed:    ed,
		cfg:   cfg,
	}
}

// Seed initializes meta/state and the founding 5 writers + 10 critics from editorial/*.md.
// When already seeded (and force is false), it safely adds any newly defined critics without altering existing data.
func (e *Engine) Seed(ctx context.Context, force bool) error {
	now := time.Now().UTC()
	founding, err := seed.FoundingAgents(e.ed, now)
	if err != nil {
		return fmt.Errorf("load founding agents: %w", err)
	}

	if !force {
		if _, err := e.store.GetState(ctx); err == nil {
			added := 0
			for _, a := range founding {
				if a.Role != model.RoleCritic {
					continue
				}
				if _, err := e.store.GetAgent(ctx, a.ID); errors.Is(
					err,
					store.ErrNotFound,
				) {
					if err := e.store.SaveAgent(ctx, a); err != nil {
						return fmt.Errorf("add critic %s: %w", a.ID, err)
					}
					added++
				}
			}
			if added > 0 {
				log.Printf("added %d new critics to existing collective", added)
			} else {
				log.Println("cornice already seeded (pass --force to re-seed state and founding agents)")
			}
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}

	state, err := seed.InitialState(
		e.ed,
		e.cfg.MaxMembers,
		e.cfg.StoriesPerIssue,
		e.cfg.IssuesPerOpenCall,
		now,
	)
	if err != nil {
		return fmt.Errorf("build initial state: %w", err)
	}
	if err := e.store.SaveState(ctx, state); err != nil {
		return fmt.Errorf("save initial state: %w", err)
	}
	for _, a := range founding {
		if err := e.store.SaveAgent(ctx, a); err != nil {
			return fmt.Errorf("save founding agent %s: %w", a.ID, err)
		}
	}
	log.Printf(
		"seeded cornice from editorial Markdown: %d founding agents, max_members=%d",
		len(founding),
		state.MaxMembers,
	)
	return nil
}

// Tick executes the single next scheduled phase under a distributed lease lock,
// persisting token usage even if the phase errors midway.
func (e *Engine) Tick(ctx context.Context) error {
	if _, err := e.loadOrInitState(ctx); err != nil {
		return err
	}

	lockOwner := makeLockOwner()
	if err := e.store.AcquireLock(ctx, lockOwner, tickLockTTL); err != nil {
		return err
	}
	defer func() {
		if err := e.store.ReleaseLock(context.Background(), lockOwner); err != nil {
			log.Printf("warning: failed to release tick lock: %v", err)
		}
	}()

	state, err := e.store.GetState(ctx)
	if err != nil {
		return err
	}
	if charter, err := e.ed.Charter(); err == nil && charter != "" {
		state.Charter = charter
	}

	// Preserve persisted state limits so transient env overrides (e.g. MAX_MEMBERS=5)
	// don't permanently overwrite Firestore configuration.
	persistedMax := state.MaxMembers
	persistedStories := state.StoriesPerIssue
	persistedOpenCall := state.IssuesPerOpenCall
	effMax := effectivePositive(e.cfg.MaxMembers, state.MaxMembers, 10)
	effStories := effectivePositive(
		e.cfg.StoriesPerIssue,
		state.StoriesPerIssue,
		2,
	)
	effOpenCall := effectivePositive(
		e.cfg.IssuesPerOpenCall,
		state.IssuesPerOpenCall,
		3,
	)

	log.Printf(
		"=== TICK: issue=%d phase=%s ===",
		state.CurrentIssue,
		state.NextPhase,
	)
	startUsage := e.adk.Usage()

	var phaseErr error
	switch state.NextPhase {
	case model.PhaseCommission:
		phaseErr = e.runCommission(ctx, &state, effStories)
	case model.PhaseDraft:
		phaseErr = e.runDraft(ctx, &state)
	case model.PhaseWorkshop:
		phaseErr = e.runWorkshop(ctx, &state)
	case model.PhasePublish:
		phaseErr = e.runPublish(ctx, &state)
	case model.PhaseReview:
		phaseErr = e.runReview(ctx, &state)
	case model.PhaseReflect:
		phaseErr = e.runReflect(ctx, &state, effOpenCall)
	case model.PhaseOpenCall:
		phaseErr = e.runOpenCall(ctx, &state, effMax, true)
	default:
		return fmt.Errorf("unknown phase: %q", state.NextPhase)
	}

	// Always record token usage deltas, even when a phase fails partway through.
	endUsage := e.adk.Usage()
	deltaCalls := endUsage.Calls - startUsage.Calls
	deltaIn := endUsage.InputTokens - startUsage.InputTokens
	deltaOut := endUsage.OutputTokens - startUsage.OutputTokens
	deltaThought := endUsage.ThoughtTokens - startUsage.ThoughtTokens

	state.MaxMembers = persistedMax
	state.StoriesPerIssue = persistedStories
	state.IssuesPerOpenCall = persistedOpenCall
	state.TokenUsage.Calls += deltaCalls
	state.TokenUsage.InputTokens += deltaIn
	state.TokenUsage.OutputTokens += deltaOut
	state.TokenUsage.ThoughtTokens += deltaThought
	state.LastTickAt = time.Now().UTC()
	state.LockOwner = lockOwner

	if saveErr := e.store.SaveState(ctx, state); saveErr != nil {
		if phaseErr != nil {
			return fmt.Errorf(
				"%w (also failed saving state: %v)",
				phaseErr,
				saveErr,
			)
		}
		return fmt.Errorf("save state: %w", saveErr)
	}
	if phaseErr != nil {
		log.Printf(
			"tick FAILED (token delta recorded: calls=%d in=%d out=%d thought=%d)",
			deltaCalls,
			deltaIn,
			deltaOut,
			deltaThought,
		)
		return phaseErr
	}
	log.Printf(
		"tick complete -> next_phase=%s | calls=%d in=%d out=%d thought=%d",
		state.NextPhase,
		deltaCalls,
		deltaIn,
		deltaOut,
		deltaThought,
	)
	return nil
}

// RunIssue executes all 6 phases of a single issue end-to-end for fast local iteration.
func (e *Engine) RunIssue(ctx context.Context) error {
	state, err := e.loadOrInitState(ctx)
	if err != nil {
		return err
	}
	if state.NextPhase == model.PhaseOpenCall {
		log.Println(
			"next_phase is open_call; running open_call first before starting the next issue",
		)
		if err := e.Tick(ctx); err != nil {
			return err
		}
		state, err = e.store.GetState(ctx)
		if err != nil {
			return err
		}
	}

	targetIssue := state.CurrentIssue
	if state.NextPhase == model.PhaseCommission {
		targetIssue++
	}
	for {
		if err := e.Tick(ctx); err != nil {
			return err
		}
		cur, err := e.store.GetState(ctx)
		if err != nil {
			return err
		}
		if cur.CurrentIssue >= targetIssue &&
			(cur.NextPhase == model.PhaseCommission || cur.NextPhase == model.PhaseOpenCall) {
			log.Printf(
				"=== ISSUE %d COMPLETE (next_phase=%s) ===",
				cur.CurrentIssue,
				cur.NextPhase,
			)
			return nil
		}
	}
}

// ForceOpenCall runs an Open Call immediately under the lease lock without clobbering
// an in-progress issue's NextPhase or permanently overwriting state.MaxMembers.
func (e *Engine) ForceOpenCall(ctx context.Context) error {
	if _, err := e.loadOrInitState(ctx); err != nil {
		return err
	}
	lockOwner := makeLockOwner()
	if err := e.store.AcquireLock(ctx, lockOwner, tickLockTTL); err != nil {
		return err
	}
	defer func() {
		if err := e.store.ReleaseLock(context.Background(), lockOwner); err != nil {
			log.Printf("warning: failed to release lock: %v", err)
		}
	}()

	state, err := e.store.GetState(ctx)
	if err != nil {
		return err
	}
	if charter, err := e.ed.Charter(); err == nil && charter != "" {
		state.Charter = charter
	}
	persistedMax := state.MaxMembers
	effMax := effectivePositive(e.cfg.MaxMembers, state.MaxMembers, 10)
	advancePhase := state.NextPhase == model.PhaseOpenCall

	startUsage := e.adk.Usage()
	callErr := e.runOpenCall(ctx, &state, effMax, advancePhase)

	endUsage := e.adk.Usage()
	state.MaxMembers = persistedMax
	state.TokenUsage.Calls += endUsage.Calls - startUsage.Calls
	state.TokenUsage.InputTokens += endUsage.InputTokens - startUsage.InputTokens
	state.TokenUsage.OutputTokens += endUsage.OutputTokens - startUsage.OutputTokens
	state.TokenUsage.ThoughtTokens += endUsage.ThoughtTokens - startUsage.ThoughtTokens
	state.LastTickAt = time.Now().UTC()
	state.LockOwner = lockOwner

	if saveErr := e.store.SaveState(ctx, state); saveErr != nil {
		if callErr != nil {
			return fmt.Errorf(
				"%w (also failed saving state: %v)",
				callErr,
				saveErr,
			)
		}
		return saveErr
	}
	return callErr
}

func (e *Engine) loadOrInitState(ctx context.Context) (model.State, error) {
	state, err := e.store.GetState(ctx)
	if errors.Is(err, store.ErrNotFound) {
		if err := e.Seed(ctx, false); err != nil {
			return model.State{}, err
		}
		return e.store.GetState(ctx)
	}
	return state, err
}

func (e *Engine) mustSkill(name string) (string, error) {
	return e.ed.Skill(name)
}

func makeLockOwner() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%d-%d", host, os.Getpid(), time.Now().UnixNano())
}

func effectivePositive(override, persisted, fallback int) int {
	if override > 0 {
		return override
	}
	if persisted > 0 {
		return persisted
	}
	return fallback
}

func filterAgents(
	all []model.Agent,
	role model.Role,
	status model.AgentStatus,
) []model.Agent {
	var out []model.Agent
	for _, a := range all {
		if a.Role == role && (status == "" || a.Status == status) {
			out = append(out, a)
		}
	}
	return out
}

func agentMap(all []model.Agent) map[string]model.Agent {
	m := make(map[string]model.Agent, len(all))
	for _, a := range all {
		m[a.ID] = a
	}
	return m
}

func applyRelationshipDelta(
	a *model.Agent,
	targetID string,
	trustDelta, respectDelta int,
	impression string,
) {
	if targetID == "" || targetID == a.ID {
		return
	}
	if a.Relationships == nil {
		a.Relationships = make(map[string]model.Relationship)
	}
	// Clamp per-turn delta to [-2, +2] so one noisy call cannot slam an axis to ±5.
	td := clamp(trustDelta, -2, 2)
	rd := clamp(respectDelta, -2, 2)

	rel := a.Relationships[targetID]
	rel.Trust = clamp(rel.Trust+td, -5, 5)
	rel.Respect = clamp(rel.Respect+rd, -5, 5)
	if strings.TrimSpace(impression) != "" {
		rel.Impression = strings.TrimSpace(impression)
	}
	a.Relationships[targetID] = rel
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var nonAlnumRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 44 {
		s = strings.TrimRight(s[:44], "-")
	}
	if s == "" {
		return "untitled"
	}
	return s
}

// uniqueStoryID guarantees no collisions even if two authors in the same issue pick identical titles.
func uniqueStoryID(issueNum int, title, authorID string) string {
	authorTag := slugify(authorID)
	if parts := strings.Split(authorTag, "-"); len(parts) > 0 {
		authorTag = parts[len(parts)-1]
	}
	if len(authorTag) > 10 {
		authorTag = authorTag[:10]
	}
	return fmt.Sprintf("issue-%04d-%s-%s", issueNum, slugify(title), authorTag)
}
