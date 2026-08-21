package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, name string, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInspectOutgoingClassifiesByExtension(t *testing.T) {
	cases := []struct {
		name     string
		wantKind string
		wantMime string
		asPhoto  bool
	}{
		{"shot.png", string(KindPhoto), "image/png", true},
		{"shot.JPG", string(KindPhoto), "image/jpeg", true},
		{"clip.mp4", string(KindVideo), "video/mp4", false},
		{"note.ogg", "audio", "audio/ogg", false},
		{"paper.pdf", string(KindDocument), "application/pdf", false},
		{"whatever.qqq", string(KindDocument), "application/octet-stream", false},
		// An animation is a document carrying the animated attribute. Sending it as a photo would
		// silently flatten it to one frame.
		{"loop.gif", string(KindGIFAnimation), "image/gif", false},
	}
	for _, tc := range cases {
		out, err := InspectOutgoing(writeTempFile(t, tc.name, 64), false)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if out.Kind != tc.wantKind || out.MimeType != tc.wantMime || out.AsPhoto != tc.asPhoto {
			t.Errorf("%s = kind %q mime %q asPhoto %v, want %q %q %v",
				tc.name, out.Kind, out.MimeType, out.AsPhoto, tc.wantKind, tc.wantMime, tc.asPhoto)
		}
		if out.FileName != tc.name {
			t.Errorf("%s: FileName = %q", tc.name, out.FileName)
		}
	}
}

// "Send as file" is the only way to keep the original pixels, so it has to win over the extension.
func TestInspectOutgoingHonoursAsFile(t *testing.T) {
	out, err := InspectOutgoing(writeTempFile(t, "shot.png", 64), true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != string(KindDocument) || out.AsPhoto {
		t.Fatalf("kind = %q asPhoto = %v, want a document", out.Kind, out.AsPhoto)
	}
	// The MIME type still describes the bytes; only the transport changes.
	if out.MimeType != "image/png" {
		t.Fatalf("MimeType = %q, want image/png", out.MimeType)
	}
}

// An image above the photo limit is still perfectly sendable as a document. Refusing it would be a
// worse answer than the one every other client gives.
func TestInspectOutgoingFallsBackToDocumentForBigImages(t *testing.T) {
	kind, asPhoto := outgoingKind("image/png", PhotoMaxBytes+1, false)
	if kind != string(KindDocument) || asPhoto {
		t.Fatalf("kind = %q asPhoto = %v, want a document", kind, asPhoto)
	}
	if kind, asPhoto := outgoingKind("image/png", PhotoMaxBytes, false); kind != string(KindPhoto) || !asPhoto {
		t.Fatalf("at the limit: kind = %q asPhoto = %v, want a photo", kind, asPhoto)
	}
}

func TestInspectOutgoingRejectsWhatCannotBeSent(t *testing.T) {
	if _, err := InspectOutgoing("", false); err == nil {
		t.Error("an empty path was accepted")
	}
	if _, err := InspectOutgoing(filepath.Join(t.TempDir(), "missing.png"), false); err == nil {
		t.Error("a missing file was accepted")
	}
	if _, err := InspectOutgoing(t.TempDir(), false); err == nil {
		t.Error("a directory was accepted")
	}
	// Almost always a half-written download, and Telegram's own error for it is unreadable.
	if _, err := InspectOutgoing(writeTempFile(t, "empty.png", 0), false); err == nil {
		t.Error("an empty file was accepted")
	}
}

// The size check has to happen before the upload, not after it.
func TestInspectOutgoingRejectsOversizeBeforeUploading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: no bytes are written, only the size is claimed.
	if err := f.Truncate(FileMaxBytes + 1); err != nil {
		_ = f.Close()
		t.Skipf("cannot create a sparse file here: %v", err)
	}
	_ = f.Close()

	_, err = InspectOutgoing(path, false)
	if err == nil {
		t.Fatal("a file over the upload limit was accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("error = %q, want it to name the limit", err)
	}
}

func TestParseProbeOutput(t *testing.T) {
	video := parseProbeOutput("1920\n1080\n12.500000\n")
	if !video.Ok || video.Width != 1920 || video.Height != 1080 || video.Duration != 12.5 {
		t.Fatalf("video probe = %+v", video)
	}
	// Audio has no video stream, so ffprobe answers with the duration alone.
	audio := parseProbeOutput("38.4\n")
	if !audio.Ok || audio.Duration != 38.4 || audio.Width != 0 {
		t.Fatalf("audio probe = %+v", audio)
	}
	// Anything unexpected must report not-ok rather than half-filled numbers: the caller sends a
	// plain document in that case, which is always valid.
	for _, raw := range []string{"", "N/A\n", "1920\n1080\n", "a\nb\nc\n"} {
		if got := parseProbeOutput(raw); got.Ok {
			t.Errorf("parseProbeOutput(%q) = %+v, want not ok", raw, got)
		}
	}
}
