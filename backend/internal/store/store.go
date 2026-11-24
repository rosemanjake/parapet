package store

import (
	"context"
	"errors"
	"time"

	"cornice/internal/model"
)

// ErrNotFound is returned when a requested document does not exist.
var ErrNotFound = errors.New("not found")

// ErrLocked is returned when another runner currently holds the tick lease.
var ErrLocked = errors.New(
	"cornice tick lock is currently held by another process",
)

// Store defines the persistence boundary implemented by Firestore and local JSON storage.
type Store interface {
	Close() error

	AcquireLock(ctx context.Context, owner string, ttl time.Duration) error
	ReleaseLock(ctx context.Context, owner string) error

	GetState(ctx context.Context) (model.State, error)
	SaveState(ctx context.Context, s model.State) error

	ListAgents(ctx context.Context) ([]model.Agent, error)
	GetAgent(ctx context.Context, id string) (model.Agent, error)
	SaveAgent(ctx context.Context, a model.Agent) error

	ListIssues(ctx context.Context) ([]model.Issue, error)
	GetIssue(ctx context.Context, number int) (model.Issue, error)
	SaveIssue(ctx context.Context, issue model.Issue) error

	ListStories(ctx context.Context) ([]model.Story, error)
	ListStoriesByIssue(
		ctx context.Context,
		issueNumber int,
	) ([]model.Story, error)
	GetStory(ctx context.Context, id string) (model.Story, error)
	SaveStory(ctx context.Context, story model.Story) error

	ListOpenCalls(ctx context.Context) ([]model.OpenCall, error)
	SaveOpenCall(ctx context.Context, oc model.OpenCall) error
}
