package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cornice/internal/model"
)

// LocalStore stores Cornice documents as indented JSON files mirroring Firestore collections.
// This lets you iterate locally with zero GCP setup and seamlessly inspect generated content.
type LocalStore struct {
	root string
}

// NewLocalStore initializes a directory-backed JSON store.
func NewLocalStore(root string) (*LocalStore, error) {
	dirs := []string{"meta", "agents", "issues", "stories", "open_calls"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, fmt.Errorf("create local store dir %s: %w", d, err)
		}
	}
	return &LocalStore{root: root}, nil
}

func (l *LocalStore) Close() error { return nil }

func (l *LocalStore) AcquireLock(
	ctx context.Context,
	owner string,
	ttl time.Duration,
) error {
	s, err := l.GetState(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if s.LockOwner != "" && s.LockOwner != owner && s.LockUntil.After(now) {
		return fmt.Errorf(
			"%w (owner=%s until=%s)",
			ErrLocked,
			s.LockOwner,
			s.LockUntil.Format(time.RFC3339),
		)
	}
	s.LockOwner = owner
	s.LockUntil = now.Add(ttl)
	return l.SaveState(ctx, s)
}

func (l *LocalStore) ReleaseLock(ctx context.Context, owner string) error {
	s, err := l.GetState(ctx)
	if err != nil {
		return nil
	}
	if s.LockOwner == owner {
		s.LockOwner = ""
		s.LockUntil = time.Time{}
		return l.SaveState(ctx, s)
	}
	return nil
}

func (l *LocalStore) GetState(_ context.Context) (model.State, error) {
	var s model.State
	err := readJSON(filepath.Join(l.root, "meta", "state.json"), &s)
	return s, err
}

func (l *LocalStore) SaveState(_ context.Context, s model.State) error {
	return writeJSON(filepath.Join(l.root, "meta", "state.json"), s)
}

func (l *LocalStore) ListAgents(_ context.Context) ([]model.Agent, error) {
	items, err := readDirJSON[model.Agent](filepath.Join(l.root, "agents"))
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].JoinedIssue != items[j].JoinedIssue {
			return items[i].JoinedIssue < items[j].JoinedIssue
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func (l *LocalStore) GetAgent(
	_ context.Context,
	id string,
) (model.Agent, error) {
	var a model.Agent
	err := readJSON(filepath.Join(l.root, "agents", id+".json"), &a)
	return a, err
}

func (l *LocalStore) SaveAgent(_ context.Context, a model.Agent) error {
	return writeJSON(filepath.Join(l.root, "agents", a.ID+".json"), a)
}

func (l *LocalStore) ListIssues(_ context.Context) ([]model.Issue, error) {
	items, err := readDirJSON[model.Issue](filepath.Join(l.root, "issues"))
	if err != nil {
		return nil, err
	}
	sort.Slice(
		items,
		func(i, j int) bool { return items[i].Number > items[j].Number },
	)
	return items, nil
}

func (l *LocalStore) GetIssue(
	_ context.Context,
	number int,
) (model.Issue, error) {
	var issue model.Issue
	err := readJSON(
		filepath.Join(l.root, "issues", issueDocID(number)+".json"),
		&issue,
	)
	return issue, err
}

func (l *LocalStore) SaveIssue(_ context.Context, issue model.Issue) error {
	return writeJSON(
		filepath.Join(l.root, "issues", issueDocID(issue.Number)+".json"),
		issue,
	)
}

func (l *LocalStore) ListStories(_ context.Context) ([]model.Story, error) {
	items, err := readDirJSON[model.Story](filepath.Join(l.root, "stories"))
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IssueNumber != items[j].IssueNumber {
			return items[i].IssueNumber > items[j].IssueNumber
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (l *LocalStore) ListStoriesByIssue(
	ctx context.Context,
	issueNumber int,
) ([]model.Story, error) {
	all, err := l.ListStories(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Story
	for _, st := range all {
		if st.IssueNumber == issueNumber {
			out = append(out, st)
		}
	}
	return out, nil
}

func (l *LocalStore) GetStory(
	_ context.Context,
	id string,
) (model.Story, error) {
	var st model.Story
	err := readJSON(filepath.Join(l.root, "stories", id+".json"), &st)
	return st, err
}

func (l *LocalStore) SaveStory(_ context.Context, st model.Story) error {
	return writeJSON(filepath.Join(l.root, "stories", st.ID+".json"), st)
}

func (l *LocalStore) ListOpenCalls(
	_ context.Context,
) ([]model.OpenCall, error) {
	items, err := readDirJSON[model.OpenCall](
		filepath.Join(l.root, "open_calls"),
	)
	if err != nil {
		return nil, err
	}
	sort.Slice(
		items,
		func(i, j int) bool { return items[i].AfterIssue > items[j].AfterIssue },
	)
	return items, nil
}

func (l *LocalStore) SaveOpenCall(_ context.Context, oc model.OpenCall) error {
	return writeJSON(filepath.Join(l.root, "open_calls", oc.ID+".json"), oc)
}

func issueDocID(number int) string {
	return fmt.Sprintf("issue-%04d", number)
}

func readJSON(path string, dst any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return json.Unmarshal(raw, dst)
}

func writeJSON(path string, val any) error {
	raw, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".cornice-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(raw); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func readDirJSON[T any](dir string) ([]T, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []T
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var item T
		if err := readJSON(filepath.Join(dir, e.Name()), &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
