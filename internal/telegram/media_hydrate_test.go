package telegram

import (
	"os"
	"path/filepath"
	"testing"
)

// A GIF's cached file is an MP4. Its thumbnail JPEG is also a preview candidate, but adopting
// that path would make O open a still image and stop the inline animator from finding the MP4.
func TestHydratePreviewKeepsAnimationLocalPath(t *testing.T) {
	dir := t.TempDir()
	mp4 := filepath.Join(dir, "document_1234.mp4")
	if err := os.WriteFile(mp4, []byte("not really an mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	thumb := filepath.Join(dir, "document_1234-m.jpg")
	if err := os.WriteFile(thumb, []byte("not really a jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Stand in for the real renderers: the pure Go still decoder fails on the MP4 (as it does
	// in practice) and ffmpeg is unavailable, so only the JPEG produces a raster.
	oldRender := renderMediaPreview
	oldStill := renderVideoStillPreview
	renderMediaPreview = func(path string) string {
		if filepath.Ext(path) == ".jpg" {
			return "\x1b[38;2;1;2;3mraster\x1b[0m"
		}
		return ""
	}
	renderVideoStillPreview = func(path string, maxCols, maxRows int) string { return "" }
	t.Cleanup(func() {
		renderMediaPreview = oldRender
		renderVideoStillPreview = oldStill
	})

	got := hydrateMediaPreviewFromDisk(dir, MediaAttachment{
		Kind:        "gif",
		DownloadKey: "document:1234",
		MimeType:    "video/mp4",
		ThumbSize:   "m",
		LocalPath:   mp4,
	})

	if !PreviewIsRaster(got.PreviewText) {
		t.Fatalf("preview = %q, want a raster from the thumbnail", got.PreviewText)
	}
	if got.LocalPath != mp4 {
		t.Fatalf("LocalPath = %q, want the animation at %q", got.LocalPath, mp4)
	}
}

// When there is no real file yet, adopting the candidate path is still the right thing.
func TestHydratePreviewAdoptsPathWhenLocalPathEmpty(t *testing.T) {
	dir := t.TempDir()
	sticker := filepath.Join(dir, "document_77.webp")
	if err := os.WriteFile(sticker, []byte("not really a webp"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldRender := renderMediaPreview
	renderMediaPreview = func(path string) string { return "\x1b[38;2;9;9;9mraster\x1b[0m" }
	t.Cleanup(func() { renderMediaPreview = oldRender })

	got := hydrateMediaPreviewFromDisk(dir, MediaAttachment{
		Kind:        "sticker",
		DownloadKey: "document:77",
		MimeType:    "image/webp",
	})

	if got.LocalPath != sticker {
		t.Fatalf("LocalPath = %q, want %q", got.LocalPath, sticker)
	}
}
