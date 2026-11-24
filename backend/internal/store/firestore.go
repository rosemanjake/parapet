package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cornice/internal/model"
)

// FirestoreStore persists Cornice collections in Google Cloud Firestore.
type FirestoreStore struct {
	client *firestore.Client
}

// NewFirestoreStore connects to Firestore (respects FIRESTORE_EMULATOR_HOST automatically).
func NewFirestoreStore(
	ctx context.Context,
	projectID, databaseID string,
) (*FirestoreStore, error) {
	if projectID == "" {
		return nil, fmt.Errorf(
			"FIRESTORE_PROJECT_ID or GOOGLE_CLOUD_PROJECT must be set",
		)
	}
	var (
		client *firestore.Client
		err    error
	)
	if databaseID == "" || databaseID == "(default)" {
		client, err = firestore.NewClient(ctx, projectID)
	} else {
		client, err = firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	}
	if err != nil {
		return nil, fmt.Errorf("init firestore client: %w", err)
	}
	return &FirestoreStore{client: client}, nil
}

func (f *FirestoreStore) Close() error {
	return f.client.Close()
}

// AcquireLock transactionally acquires a time-bounded lease on meta/state.
func (f *FirestoreStore) AcquireLock(
	ctx context.Context,
	owner string,
	ttl time.Duration,
) error {
	ref := f.client.Collection("meta").Doc("state")
	return f.client.RunTransaction(
		ctx,
		func(ctx context.Context, tx *firestore.Transaction) error {
			snap, err := tx.Get(ref)
			if err != nil {
				if status.Code(err) == codes.NotFound {
					return ErrNotFound
				}
				return err
			}
			var s model.State
			if err := snap.DataTo(&s); err != nil {
				return err
			}
			now := time.Now().UTC()
			if s.LockOwner != "" && s.LockOwner != owner &&
				s.LockUntil.After(now) {
				return fmt.Errorf(
					"%w (owner=%s until=%s)",
					ErrLocked,
					s.LockOwner,
					s.LockUntil.Format(time.RFC3339),
				)
			}
			s.LockOwner = owner
			s.LockUntil = now.Add(ttl)
			return tx.Set(ref, s)
		},
	)
}

// ReleaseLock clears the lease on meta/state if held by owner.
func (f *FirestoreStore) ReleaseLock(ctx context.Context, owner string) error {
	ref := f.client.Collection("meta").Doc("state")
	return f.client.RunTransaction(
		ctx,
		func(ctx context.Context, tx *firestore.Transaction) error {
			snap, err := tx.Get(ref)
			if err != nil {
				if status.Code(err) == codes.NotFound {
					return nil
				}
				return err
			}
			var s model.State
			if err := snap.DataTo(&s); err != nil {
				return err
			}
			if s.LockOwner == owner {
				s.LockOwner = ""
				s.LockUntil = time.Time{}
				return tx.Set(ref, s)
			}
			return nil
		},
	)
}

func (f *FirestoreStore) GetState(ctx context.Context) (model.State, error) {
	var s model.State
	snap, err := f.client.Collection("meta").Doc("state").Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return s, ErrNotFound
		}
		return s, err
	}
	if err := snap.DataTo(&s); err != nil {
		return s, err
	}
	return s, nil
}

func (f *FirestoreStore) SaveState(ctx context.Context, s model.State) error {
	_, err := f.client.Collection("meta").Doc("state").Set(ctx, s)
	return err
}

func (f *FirestoreStore) ListAgents(
	ctx context.Context,
) ([]model.Agent, error) {
	iter := f.client.Collection("agents").Documents(ctx)
	defer iter.Stop()

	var out []model.Agent
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var a model.Agent
		if err := doc.DataTo(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].JoinedIssue != out[j].JoinedIssue {
			return out[i].JoinedIssue < out[j].JoinedIssue
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (f *FirestoreStore) GetAgent(
	ctx context.Context,
	id string,
) (model.Agent, error) {
	var a model.Agent
	snap, err := f.client.Collection("agents").Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return a, ErrNotFound
		}
		return a, err
	}
	if err := snap.DataTo(&a); err != nil {
		return a, err
	}
	return a, nil
}

func (f *FirestoreStore) SaveAgent(ctx context.Context, a model.Agent) error {
	_, err := f.client.Collection("agents").Doc(a.ID).Set(ctx, a)
	return err
}

func (f *FirestoreStore) ListIssues(
	ctx context.Context,
) ([]model.Issue, error) {
	iter := f.client.Collection("issues").Documents(ctx)
	defer iter.Stop()

	var out []model.Issue
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var issue model.Issue
		if err := doc.DataTo(&issue); err != nil {
			return nil, err
		}
		out = append(out, issue)
	}
	sort.Slice(
		out,
		func(i, j int) bool { return out[i].Number > out[j].Number },
	)
	return out, nil
}

func (f *FirestoreStore) GetIssue(
	ctx context.Context,
	number int,
) (model.Issue, error) {
	var issue model.Issue
	snap, err := f.client.Collection("issues").Doc(issueDocID(number)).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return issue, ErrNotFound
		}
		return issue, err
	}
	if err := snap.DataTo(&issue); err != nil {
		return issue, err
	}
	return issue, nil
}

func (f *FirestoreStore) SaveIssue(
	ctx context.Context,
	issue model.Issue,
) error {
	_, err := f.client.Collection("issues").
		Doc(issueDocID(issue.Number)).
		Set(ctx, issue)
	return err
}

func (f *FirestoreStore) ListStories(
	ctx context.Context,
) ([]model.Story, error) {
	iter := f.client.Collection("stories").Documents(ctx)
	defer iter.Stop()

	var out []model.Story
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var st model.Story
		if err := doc.DataTo(&st); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IssueNumber != out[j].IssueNumber {
			return out[i].IssueNumber > out[j].IssueNumber
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (f *FirestoreStore) ListStoriesByIssue(
	ctx context.Context,
	issueNumber int,
) ([]model.Story, error) {
	iter := f.client.Collection("stories").
		Where("issue_number", "==", issueNumber).
		Documents(ctx)
	defer iter.Stop()

	var out []model.Story
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var st model.Story
		if err := doc.DataTo(&st); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	sort.Slice(
		out,
		func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) },
	)
	return out, nil
}

func (f *FirestoreStore) GetStory(
	ctx context.Context,
	id string,
) (model.Story, error) {
	var st model.Story
	snap, err := f.client.Collection("stories").Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return st, ErrNotFound
		}
		return st, err
	}
	if err := snap.DataTo(&st); err != nil {
		return st, err
	}
	return st, nil
}

func (f *FirestoreStore) SaveStory(ctx context.Context, st model.Story) error {
	_, err := f.client.Collection("stories").Doc(st.ID).Set(ctx, st)
	return err
}

func (f *FirestoreStore) ListOpenCalls(
	ctx context.Context,
) ([]model.OpenCall, error) {
	iter := f.client.Collection("open_calls").Documents(ctx)
	defer iter.Stop()

	var out []model.OpenCall
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var oc model.OpenCall
		if err := doc.DataTo(&oc); err != nil {
			return nil, err
		}
		out = append(out, oc)
	}
	sort.Slice(
		out,
		func(i, j int) bool { return out[i].AfterIssue > out[j].AfterIssue },
	)
	return out, nil
}

func (f *FirestoreStore) SaveOpenCall(
	ctx context.Context,
	oc model.OpenCall,
) error {
	_, err := f.client.Collection("open_calls").Doc(oc.ID).Set(ctx, oc)
	return err
}
