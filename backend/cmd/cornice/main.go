package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"cornice/editorial"
	"cornice/internal/adkrun"
	"cornice/internal/config"
	"cornice/internal/engine"
	"cornice/internal/model"
	"cornice/internal/store"
)

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	cfg := config.Load()
	ctx := context.Background()

	st, err := openStore(ctx, cfg)
	if err != nil {
		log.Fatalf("store error: %v", err)
	}
	defer st.Close()

	ed := editorial.NewLoader(cfg.EditorialDir)

	switch cmd {
	case "seed":
		fs := flag.NewFlagSet("seed", flag.ExitOnError)
		force := fs.Bool(
			"force",
			false,
			"overwrite existing state and founding agents",
		)
		_ = fs.Parse(os.Args[2:])
		eng := engine.New(st, nil, ed, cfg)
		if err := eng.Seed(ctx, *force); err != nil {
			log.Fatalf("seed failed: %v", err)
		}

	case "status":
		if err := printStatus(ctx, st, cfg); err != nil {
			log.Fatalf("status failed: %v", err)
		}

	case "tick", "run-issue", "open-call", "backfill":
		adkRuntime, err := adkrun.New(ctx, cfg)
		if err != nil {
			log.Fatalf("ADK init failed: %v", err)
		}
		eng := engine.New(st, adkRuntime, ed, cfg)
		switch cmd {
		case "tick":
			err = eng.Tick(ctx)
		case "run-issue":
			err = eng.RunIssue(ctx)
		case "open-call":
			err = eng.ForceOpenCall(ctx)
		case "backfill":
			err = eng.BackfillAssets(ctx)
		}
		if err != nil {
			log.Fatalf("%s failed: %v", cmd, err)
		}

	default:
		usage()
		os.Exit(1)
	}
}

func openStore(ctx context.Context, cfg config.Config) (store.Store, error) {
	if cfg.LocalDataDir != "" {
		log.Printf("using local JSON store: %s", cfg.LocalDataDir)
		return store.NewLocalStore(cfg.LocalDataDir)
	}
	log.Printf(
		"using Firestore project=%s database=%s",
		cfg.ProjectID,
		cfg.DatabaseID,
	)
	return store.NewFirestoreStore(ctx, cfg.ProjectID, cfg.DatabaseID)
}

func printStatus(ctx context.Context, st store.Store, cfg config.Config) error {
	state, err := st.GetState(ctx)
	if err != nil {
		return err
	}
	agents, err := st.ListAgents(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("CORNICE STATUS\n")
	fmt.Printf(
		"  Runtime:     ADK Go v2 (%s / %s, thinking=%s)\n",
		cfg.GeminiBackend,
		cfg.GeminiModel,
		cfg.ThinkingLevel,
	)
	fmt.Printf("  Issue:       %d\n", state.CurrentIssue)
	fmt.Printf("  Next Phase:  %s\n", state.NextPhase)
	fmt.Printf(
		"  Cap:         max %d members | %d stories/issue | open call every %d issues | max %d archive reads\n",
		state.MaxMembers,
		state.StoriesPerIssue,
		state.IssuesPerOpenCall,
		cfg.MaxArchiveReads,
	)
	fmt.Printf(
		"  Token Spend: %d calls | %d input | %d output | %d thought\n",
		state.TokenUsage.Calls,
		state.TokenUsage.InputTokens,
		state.TokenUsage.OutputTokens,
		state.TokenUsage.ThoughtTokens,
	)
	fmt.Printf("  Queue:       %s\n\n", strings.Join(state.AuthorQueue, " -> "))

	fmt.Println("ACTIVE WRITERS:")
	for _, a := range agents {
		if a.Role == model.RoleWriter && a.Status == model.StatusActive {
			fmt.Printf(
				"  - %s (%s) [stories reviewed: %d]\n",
				a.Name,
				a.Genre,
				len(a.Standing),
			)
		}
	}
	fmt.Println("\nPERMANENT CRITICS:")
	for _, a := range agents {
		if a.Role == model.RoleCritic {
			fmt.Printf("  - %s (%s)\n", a.Name, a.Genre)
		}
	}
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: cornice <command>

Commands:
  seed [--force]   Initialize Cornice charter, founding writers, and critics from editorial/*.md
  tick             Advance Cornice by one scheduled phase (for Cloud Scheduler)
  run-issue        Run a complete Issue end-to-end (commission -> reflect)
  open-call        Trigger an Open Call (applicants + admission/ejection/retention votes) now
  status           Print current magazine state, roster, and token totals`)
}
