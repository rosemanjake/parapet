package media

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/genai"

	"cornice/internal/config"
)

// HouseImageStyle defines Cornice's uniform 35mm editorial photography aesthetic.
const HouseImageStyle = "35mm film, 50mm lens, cinematic composition, understated, simple, natural light, muted palette, subtle film grain, no text, no watermarks, no borders."

// BuildPrompt combines a concrete story scene brief with Cornice's house photography style.
func BuildPrompt(scene string) string {
	scene = strings.TrimSpace(scene)
	scene = strings.TrimRight(scene, ".")
	return scene + ". " + HouseImageStyle
}

// GenerateStoryImage calls Nano Banana (gemini-3.1-flash-image) to render a 3:2 editorial photograph.
func GenerateStoryImage(
	ctx context.Context,
	cfg config.Config,
	prompt string,
) ([]byte, string, error) {
	var clientCfg *genai.ClientConfig
	if cfg.GeminiBackend == "vertex" {
		clientCfg = &genai.ClientConfig{
			Backend:  genai.BackendVertexAI,
			Project:  cfg.ProjectID,
			Location: cfg.VertexLocation,
		}
	} else {
		clientCfg = &genai.ClientConfig{
			Backend: genai.BackendGeminiAPI,
			APIKey:  cfg.GeminiAPIKey,
		}
	}

	sdk, err := genai.NewClient(ctx, clientCfg)
	if err != nil {
		return nil, "", fmt.Errorf("create genai image client: %w", err)
	}

	quality := int32(85)
	imgCfg := &genai.ImageConfig{
		AspectRatio: "3:2",
	}
	if cfg.GeminiBackend == "vertex" {
		imgCfg.OutputMIMEType = "image/jpeg"
		imgCfg.OutputCompressionQuality = &quality
	}

	genCfg := &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE"},
		ImageConfig:        imgCfg,
	}

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*attempt*4) * time.Second
			log.Printf(
				"  ! image gen retry %d in %s (%v)",
				attempt+1,
				backoff,
				lastErr,
			)
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(backoff):
			}
		}

		callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		resp, err := sdk.Models.GenerateContent(
			callCtx,
			cfg.ImageModel,
			genai.Text(prompt),
			genCfg,
		)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}

		for _, cand := range resp.Candidates {
			if cand == nil || cand.Content == nil {
				continue
			}
			for _, part := range cand.Content.Parts {
				if part != nil && part.InlineData != nil &&
					len(part.InlineData.Data) > 0 {
					mime := part.InlineData.MIMEType
					if mime == "" {
						mime = "image/jpeg"
					}
					return part.InlineData.Data, mime, nil
				}
			}
		}
		lastErr = fmt.Errorf(
			"image model %s returned no inline image bytes",
			cfg.ImageModel,
		)
	}
	return nil, "", fmt.Errorf(
		"generate image failed after retries: %w",
		lastErr,
	)
}

// UploadStoryImage stores image bytes in GCS (or local media dir when LOCAL_DATA_DIR is set)
// and returns the public URL.
func UploadStoryImage(
	ctx context.Context,
	cfg config.Config,
	slug string,
	data []byte,
	mimeType string,
) (string, error) {
	ext := ".jpg"
	if strings.Contains(mimeType, "png") {
		ext = ".png"
	} else if strings.Contains(mimeType, "webp") {
		ext = ".webp"
	}

	if cfg.LocalDataDir != "" {
		dir := filepath.Join(cfg.LocalDataDir, "media")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		fileName := slug + ext
		if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err != nil {
			return "", err
		}
		return "/media/" + fileName, nil
	}

	if strings.TrimSpace(cfg.MediaBucket) == "" {
		return "", fmt.Errorf("MEDIA_BUCKET is required to upload story images")
	}

	client, err := storage.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("create GCS client: %w", err)
	}
	defer client.Close()

	objectName := fmt.Sprintf("stories/%s%s", slug, ext)
	w := client.Bucket(cfg.MediaBucket).Object(objectName).NewWriter(ctx)
	w.ContentType = mimeType
	w.CacheControl = "public, max-age=31536000, immutable"
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return "", fmt.Errorf("write GCS object %s: %w", objectName, err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("close GCS object %s: %w", objectName, err)
	}

	return fmt.Sprintf(
		"https://storage.googleapis.com/%s/%s",
		cfg.MediaBucket,
		objectName,
	), nil
}
