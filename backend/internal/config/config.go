package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds runtime settings for the Cornice simulation runner.
type Config struct {
	GeminiBackend     string // "vertex" or "api"
	GeminiAPIKey      string
	GeminiModel       string
	ImageModel        string
	ThinkingLevel     string
	VertexLocation    string
	ProjectID         string
	DatabaseID        string
	MediaBucket       string
	LocalDataDir      string
	EditorialDir      string
	MaxArchiveReads   int
	MaxMembers        int
	StoriesPerIssue   int
	IssuesPerOpenCall int
}

// Load reads configuration from optional .env files and process environment.
func Load() Config {
	loadDotEnv(".env")
	loadDotEnv(filepath.Join("..", ".env"))

	apiKey := firstNonEmpty(
		os.Getenv("GEMINI_API_KEY"),
		os.Getenv("GOOGLE_API_KEY"),
	)
	projectID := firstNonEmpty(
		os.Getenv("GOOGLE_CLOUD_PROJECT"),
		os.Getenv("FIRESTORE_PROJECT_ID"),
	)

	backend := strings.ToLower(strings.TrimSpace(os.Getenv("GEMINI_BACKEND")))
	if backend == "" {
		useVertex := strings.ToLower(
			strings.TrimSpace(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI")),
		)
		if useVertex == "1" || useVertex == "true" || projectID != "" {
			backend = "vertex"
		} else {
			backend = "api"
		}
	}

	mediaBucket := strings.TrimSpace(os.Getenv("MEDIA_BUCKET"))
	if mediaBucket == "" && projectID != "" {
		mediaBucket = projectID + "-media"
	}

	return Config{
		GeminiBackend: backend,
		GeminiAPIKey:  apiKey,
		GeminiModel:   envOrDefault("GEMINI_MODEL", "gemini-4-neon"),
		ImageModel:    envOrDefault("IMAGE_MODEL", "gemini-3.1-flash-image"),
		ThinkingLevel: strings.ToLower(
			envOrDefault("GEMINI_THINKING_LEVEL", "medium"),
		),
		VertexLocation:    envOrDefault("VERTEX_LOCATION", "global"),
		ProjectID:         projectID,
		DatabaseID:        envOrDefault("FIRESTORE_DATABASE_ID", "(default)"),
		MediaBucket:       mediaBucket,
		LocalDataDir:      strings.TrimSpace(os.Getenv("LOCAL_DATA_DIR")),
		EditorialDir:      strings.TrimSpace(os.Getenv("EDITORIAL_DIR")),
		MaxArchiveReads:   envInt("MAX_ARCHIVE_READS", 3),
		MaxMembers:        envInt("MAX_MEMBERS", 10),
		StoriesPerIssue:   envInt("STORIES_PER_ISSUE", 2),
		IssuesPerOpenCall: envInt("ISSUES_PER_OPEN_CALL", 3),
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 &&
			((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		} else if idx := strings.Index(v, " #"); idx >= 0 {
			v = strings.TrimSpace(v[:idx])
		}
		if k != "" && os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
}
