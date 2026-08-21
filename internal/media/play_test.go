package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlayableAudioKind(t *testing.T) {
	for _, kind := range []string{"voice", "audio"} {
		if !PlayableAudioKind(kind) {
			t.Errorf("%q should be playable", kind)
		}
	}
	for _, kind := range []string{"", "photo", "video", "document", "gif", "sticker"} {
		if PlayableAudioKind(kind) {
			t.Errorf("%q should not take the audio path", kind)
		}
	}
}

func TestPlayAudioRejectsMissingFiles(t *testing.T) {
	if err := PlayAudio(""); err == nil {
		t.Error("an empty path was accepted")
	}
	if err := PlayAudio(filepath.Join(t.TempDir(), "gone.ogg")); err == nil {
		t.Error("a missing file was accepted")
	}
}

// Without a desktop handler and without a CLI player, the failure has to name what to install rather
// than looking like the file was broken.
func TestPlayAudioExplainsWhatIsMissing(t *testing.T) {
	if desktopHandlerAvailable() || consolePlayer().Name != "" {
		t.Skip("this machine can play audio, so the missing-player message cannot be observed")
	}
	path := filepath.Join(t.TempDir(), "note.ogg")
	if err := os.WriteFile(path, []byte("bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := PlayAudio(path)
	if err == nil {
		t.Fatal("no player is available, so this should have failed")
	}
	if !contains(err.Error(), "mpv") {
		t.Fatalf("error = %q, want it to name a player to install", err)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
