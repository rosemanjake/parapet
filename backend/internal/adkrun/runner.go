package adkrun

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"cornice/internal/config"
	"cornice/internal/model"
)

const perInvocationTimeout = 4 * time.Minute

// Runtime wraps Google ADK Go v2 (llmagent + runner + Vertex/Gemini model) with token accounting.
type Runtime struct {
	llmModel      adkmodel.LLM
	modelName     string
	thinkingLevel genai.ThinkingLevel

	mu    sync.Mutex
	usage model.TokenUsage
}

// Request describes one ADK agent invocation.
type Request struct {
	AgentName           string
	Instruction         string
	UserMessage         string
	Temperature         float32
	Tools               []tool.Tool
	BeforeToolCallbacks []llmagent.BeforeToolCallback
	MaxLLMCalls         int
	OnAttemptReset      func()
}

type actionAck struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// New initializes an ADK v2 Gemini model backed by Vertex AI (default) or a Gemini API key.
func New(ctx context.Context, cfg config.Config) (*Runtime, error) {
	var clientCfg *genai.ClientConfig
	if cfg.GeminiBackend == "vertex" {
		if strings.TrimSpace(cfg.ProjectID) == "" {
			return nil, fmt.Errorf(
				"GOOGLE_CLOUD_PROJECT (or FIRESTORE_PROJECT_ID) is required for Vertex AI",
			)
		}
		log.Printf(
			"ADK v2 using Vertex AI: project=%s location=%s model=%s thinking=%s",
			cfg.ProjectID,
			cfg.VertexLocation,
			cfg.GeminiModel,
			cfg.ThinkingLevel,
		)
		clientCfg = &genai.ClientConfig{
			Backend:  genai.BackendVertexAI,
			Project:  cfg.ProjectID,
			Location: cfg.VertexLocation,
		}
	} else {
		if strings.TrimSpace(cfg.GeminiAPIKey) == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY is required when GEMINI_BACKEND=api")
		}
		log.Printf("ADK v2 using Gemini API: model=%s thinking=%s", cfg.GeminiModel, cfg.ThinkingLevel)
		clientCfg = &genai.ClientConfig{
			Backend: genai.BackendGeminiAPI,
			APIKey:  cfg.GeminiAPIKey,
		}
	}

	m, err := gemini.NewModel(ctx, cfg.GeminiModel, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("create ADK gemini model: %w", err)
	}

	return &Runtime{
		llmModel:      m,
		modelName:     cfg.GeminiModel,
		thinkingLevel: parseThinkingLevel(cfg.ThinkingLevel),
	}, nil
}

func parseThinkingLevel(level string) genai.ThinkingLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "minimal":
		return genai.ThinkingLevelMinimal
	case "low":
		return genai.ThinkingLevelLow
	case "high":
		return genai.ThinkingLevelHigh
	default:
		return genai.ThinkingLevelMedium
	}
}

// Usage returns cumulative token usage recorded across all ADK model calls.
func (r *Runtime) Usage() model.TokenUsage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage
}

// RunText executes an ADK llmagent for freeform long-form prose (stories, workshop letters)
// and rejects truncated (MAX_TOKENS) or blocked (SAFETY) generations.
func (r *Runtime) RunText(ctx context.Context, req Request) (string, error) {
	if req.MaxLLMCalls <= 0 {
		req.MaxLLMCalls = 2
	}
	var lastErr error
	maxAttempts := 6
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, attempt, isRateLimitErr(lastErr)); err != nil {
				return "", err
			}
		}
		if req.OnAttemptReset != nil {
			req.OnAttemptReset()
		}
		out, finishReason, err := r.runOnce(ctx, req)
		if err != nil {
			lastErr = err
			if !isRateLimitErr(err) && attempt >= 2 {
				break
			}
			continue
		}
		if finishReason == genai.FinishReasonMaxTokens ||
			finishReason == genai.FinishReasonSafety ||
			finishReason == genai.FinishReasonRecitation {
			lastErr = fmt.Errorf(
				"generation stopped abnormally: finish_reason=%s",
				finishReason,
			)
			log.Printf(
				"  ! %s attempt %d %v, retrying...",
				req.AgentName,
				attempt+1,
				lastErr,
			)
			if attempt >= 2 {
				break
			}
			continue
		}
		if strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out), nil
		}
		lastErr = fmt.Errorf("empty text response")
		if attempt >= 2 {
			break
		}
	}
	return "", fmt.Errorf(
		"ADK agent %s (%s) failed after retries: %w",
		req.AgentName,
		r.modelName,
		lastErr,
	)
}

// RunAction executes an ADK llmagent with a typed action functiontool whose Go struct enforces
// the output structure. Optional validate rejects bad arguments so the agent can self-correct.
// On valid submission, SkipSummarization ends the turn immediately without an extra LLM call.
func RunAction[T any](
	ctx context.Context,
	r *Runtime,
	req Request,
	toolName string,
	toolDesc string,
	validate func(T) error,
) (T, error) {
	var zero T
	var lastErr error

	if req.MaxLLMCalls <= 0 {
		if len(req.Tools) > 0 {
			req.MaxLLMCalls = 8
		} else {
			req.MaxLLMCalls = 4
		}
	}

	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, attempt, isRateLimitErr(lastErr)); err != nil {
				return zero, err
			}
		}
		if req.OnAttemptReset != nil {
			req.OnAttemptReset()
		}

		var (
			mu        sync.Mutex
			captured  T
			submitted bool
		)

		actionTool, err := functiontool.New(
			functiontool.Config{
				Name:        toolName,
				Description: toolDesc,
			},
			func(tctx agent.Context, args T) (actionAck, error) {
				mu.Lock()
				already := submitted
				mu.Unlock()
				if already {
					tctx.Actions().SkipSummarization = true
					return actionAck{Status: "already_recorded"}, nil
				}
				if validate != nil {
					if err := validate(args); err != nil {
						return actionAck{
							Status:  "invalid",
							Message: err.Error() + " Please fix and call " + toolName + " again.",
						}, nil
					}
				}
				mu.Lock()
				if !submitted {
					captured = args
					submitted = true
				}
				mu.Unlock()
				tctx.Actions().SkipSummarization = true
				return actionAck{Status: "recorded"}, nil
			},
		)
		if err != nil {
			return zero, fmt.Errorf("create action tool %s: %w", toolName, err)
		}

		attemptReq := req
		attemptReq.Tools = append(
			append([]tool.Tool(nil), req.Tools...),
			actionTool,
		)

		if _, _, err := r.runOnce(ctx, attemptReq); err != nil {
			lastErr = err
			if !isRateLimitErr(err) && attempt >= 2 {
				break
			}
			continue
		}

		mu.Lock()
		ok := submitted
		res := captured
		mu.Unlock()
		if ok {
			return res, nil
		}
		lastErr = fmt.Errorf(
			"agent %s finished without calling required tool %s",
			req.AgentName,
			toolName,
		)
		if attempt >= 2 {
			break
		}
	}

	return zero, fmt.Errorf(
		"ADK action %s (%s) failed after retries: %w",
		toolName,
		req.AgentName,
		lastErr,
	)
}

func (r *Runtime) runOnce(
	ctx context.Context,
	req Request,
) (string, genai.FinishReason, error) {
	runCtx, cancel := context.WithTimeout(ctx, perInvocationTimeout)
	defer cancel()

	temp := req.Temperature
	genCfg := &genai.GenerateContentConfig{
		Temperature:    &temp,
		ThinkingConfig: &genai.ThinkingConfig{ThinkingLevel: r.thinkingLevel},
	}

	agentName := sanitizeAgentName(req.AgentName)
	instruction := req.Instruction

	a, err := llmagent.New(llmagent.Config{
		Name:                  agentName,
		Model:                 r.llmModel,
		Description:           "Cornice literary collective member or critic.",
		GenerateContentConfig: genCfg,
		InstructionProvider: func(_ agent.ReadonlyContext) (string, error) {
			return instruction, nil
		},
		Tools:               req.Tools,
		BeforeToolCallbacks: req.BeforeToolCallbacks,
		AfterModelCallbacks: []llmagent.AfterModelCallback{
			func(_ agent.Context, resp *adkmodel.LLMResponse, err error) (*adkmodel.LLMResponse, error) {
				if err == nil && resp != nil {
					r.recordUsage(resp.UsageMetadata)
				}
				return nil, nil
			},
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("construct ADK llmagent: %w", err)
	}

	run, err := runner.New(runner.Config{
		AppName:           "cornice",
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return "", "", fmt.Errorf("construct ADK runner: %w", err)
	}

	sessionID := fmt.Sprintf("%s-%d", agentName, time.Now().UnixNano())
	msg := genai.NewContentFromText(req.UserMessage, genai.RoleUser)

	runCfg := agent.RunConfig{
		MaxLLMCalls: req.MaxLLMCalls,
	}

	var finalText strings.Builder
	var lastNonEmpty string
	var lastFinish genai.FinishReason
	for ev, err := range run.Run(runCtx, "cornice", sessionID, msg, runCfg) {
		if err != nil {
			return "", lastFinish, err
		}
		if ev == nil {
			continue
		}
		if ev.ErrorMessage != "" {
			return "", lastFinish, fmt.Errorf(
				"model error (%s): %s",
				ev.ErrorCode,
				ev.ErrorMessage,
			)
		}
		if ev.FinishReason != "" {
			lastFinish = ev.FinishReason
		}
		text := extractEventText(ev)
		if text != "" {
			lastNonEmpty = text
		}
		if ev.IsFinalResponse() && text != "" {
			finalText.Reset()
			finalText.WriteString(text)
		}
	}

	out := strings.TrimSpace(finalText.String())
	if out == "" {
		out = strings.TrimSpace(lastNonEmpty)
	}
	return out, lastFinish, nil
}

func (r *Runtime) recordUsage(u *genai.GenerateContentResponseUsageMetadata) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage.Calls++
	if u != nil {
		r.usage.InputTokens += int64(u.PromptTokenCount)
		r.usage.OutputTokens += int64(u.CandidatesTokenCount)
		r.usage.ThoughtTokens += int64(u.ThoughtsTokenCount)
	}
}

func isRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "RESOURCE_EXHAUSTED") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "UNAVAILABLE")
}

func sleepBackoff(ctx context.Context, attempt int, rateLimited bool) error {
	var backoff time.Duration
	if rateLimited {
		// Longer backoff to clear per-minute Vertex AI quota windows cleanly.
		schedule := []time.Duration{
			8 * time.Second,
			16 * time.Second,
			30 * time.Second,
			45 * time.Second,
			65 * time.Second,
		}
		idx := attempt - 1
		if idx >= len(schedule) {
			idx = len(schedule) - 1
		}
		backoff = schedule[idx]
		log.Printf(
			"  ! rate-limited (429/503); backing off %s before retry %d...",
			backoff,
			attempt+1,
		)
	} else {
		backoff = time.Duration(attempt*attempt) * 2 * time.Second
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(backoff):
		return nil
	}
}

func extractEventText(ev *session.Event) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range ev.Content.Parts {
		if p == nil || p.Thought {
			continue
		}
		if p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func sanitizeAgentName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' {
			b.WriteRune(ch)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" || out == "user" {
		return "cornice_agent"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "a_" + out
	}
	return out
}
