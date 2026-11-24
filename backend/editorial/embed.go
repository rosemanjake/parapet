package editorial

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cornice/internal/model"
)

//go:embed charter.md writers/*.md critics/*.md skills/*/SKILL.md
var embeddedFS embed.FS

// Loader reads Cornice's Charter, Personas, and SKILL.md files from disk (when EDITORIAL_DIR is set)
// or from the binary's embedded Markdown files.
type Loader struct {
	fsys fs.FS
}

// NewLoader creates a Loader backed by diskDir if non-empty, otherwise embedded Markdown files.
func NewLoader(diskDir string) *Loader {
	if strings.TrimSpace(diskDir) != "" {
		return &Loader{fsys: os.DirFS(diskDir)}
	}
	return &Loader{fsys: embeddedFS}
}

// Charter returns the Cornice House Editorial Charter Markdown.
func (l *Loader) Charter() (string, error) {
	raw, err := fs.ReadFile(l.fsys, "charter.md")
	if err != nil {
		return "", fmt.Errorf("read charter.md: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// Skill returns the body of skills/<name>/SKILL.md with YAML frontmatter stripped.
func (l *Loader) Skill(name string) (string, error) {
	path := filepath.ToSlash(filepath.Join("skills", name, "SKILL.md"))
	raw, err := fs.ReadFile(l.fsys, path)
	if err != nil {
		return "", fmt.Errorf("read skill %s: %w", name, err)
	}
	_, body := splitFrontmatter(string(raw))
	return strings.TrimSpace(body), nil
}

// FoundingAgents loads all writer and critic Markdown files from writers/ and critics/.
func (l *Loader) FoundingAgents(now time.Time) ([]model.Agent, error) {
	var agents []model.Agent
	for _, dir := range []string{"writers", "critics"} {
		entries, err := fs.ReadDir(l.fsys, dir)
		if err != nil {
			return nil, fmt.Errorf("read editorial/%s: %w", dir, err)
		}
		sort.Slice(
			entries,
			func(i, j int) bool { return entries[i].Name() < entries[j].Name() },
		)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			raw, err := fs.ReadFile(l.fsys, dir+"/"+e.Name())
			if err != nil {
				return nil, fmt.Errorf("read %s/%s: %w", dir, e.Name(), err)
			}
			a, err := parseAgentMarkdown(string(raw), now)
			if err != nil {
				return nil, fmt.Errorf("parse %s/%s: %w", dir, e.Name(), err)
			}
			agents = append(agents, a)
		}
	}
	return agents, nil
}

func parseAgentMarkdown(src string, now time.Time) (model.Agent, error) {
	meta, body := splitFrontmatter(src)
	sections := parseSections(body)

	role := model.Role(strings.ToLower(meta["role"]))
	if role != model.RoleWriter && role != model.RoleCritic {
		return model.Agent{}, fmt.Errorf("invalid role %q", meta["role"])
	}
	id := strings.TrimSpace(meta["id"])
	name := strings.TrimSpace(meta["name"])
	if id == "" || name == "" {
		return model.Agent{}, fmt.Errorf(
			"missing required id or name in frontmatter",
		)
	}

	a := model.Agent{
		ID:                   id,
		Name:                 name,
		Role:                 role,
		Status:               model.StatusActive,
		Genre:                strings.TrimSpace(meta["genre"]),
		Bio:                  sections["Bio"],
		VoiceCraft:           sections["Voice & Craft"],
		NegativeRules:        sections["Negative Rules"],
		EditorialTemperament: sections["Editorial Temperament"],
		Notebook:             sections["Initial Notebook"],
		IsFounder:            strings.EqualFold(meta["founder"], "true"),
		JoinedIssue:          0,
		CreatedAt:            now,
	}

	if relText := sections["Relationships"]; relText != "" {
		a.Relationships = map[string]model.Relationship{}
		for _, entry := range parseBulletEntries(relText) {
			parts := strings.SplitN(entry, "|", 4)
			if len(parts) < 4 {
				continue
			}
			targetID := strings.TrimSpace(parts[0])
			trust, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			respect, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
			impression := strings.TrimSpace(parts[3])
			a.Relationships[targetID] = model.Relationship{
				Trust:      trust,
				Respect:    respect,
				Impression: impression,
			}
		}
	}
	return a, nil
}

func splitFrontmatter(src string) (map[string]string, string) {
	meta := map[string]string{}
	trimmed := strings.TrimLeft(src, "\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return meta, src
	}
	rest := strings.TrimPrefix(trimmed, "---")
	rest = strings.TrimLeft(rest, "\r\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return meta, src
	}
	fmBlock := rest[:idx]
	body := strings.TrimLeft(rest[idx+4:], "\r\n")

	for _, line := range strings.Split(fmBlock, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return meta, body
}

func parseSections(body string) map[string]string {
	out := map[string]string{}
	var curTitle string
	var buf []string
	flush := func() {
		if curTitle != "" {
			out[curTitle] = strings.TrimSpace(strings.Join(buf, "\n"))
		}
		buf = nil
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			curTitle = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		if curTitle != "" {
			buf = append(buf, line)
		}
	}
	flush()
	return out
}

// parseBulletEntries joins 80-col wrapped Markdown list continuations onto their bullet item.
func parseBulletEntries(block string) []string {
	var entries []string
	for _, raw := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			entries = append(
				entries,
				strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")),
			)
		} else if len(entries) > 0 {
			entries[len(entries)-1] += " " + trimmed
		}
	}
	return entries
}
