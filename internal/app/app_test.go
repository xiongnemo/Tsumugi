package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/storage"
)

func TestClearTelegramSessionFilesOnlyRemovesSessionFiles(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions")
	mediaDir := filepath.Join(root, "media")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(sessionDir, "user.json")
	mediaFile := filepath.Join(mediaDir, "photo.jpg")
	if err := os.WriteFile(sessionFile, []byte("session"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaFile, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := clearTelegramSessionFiles(sessionDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
		t.Fatalf("session file still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(mediaFile); err != nil {
		t.Fatalf("media file should be kept: %v", err)
	}
}

func TestStoredTelegramConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, _, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db.SetCipher(cipher)

	saved := config.Config{
		AuthMode: config.AuthBot,
		APIID:    12345,
		APIHash:  "hash",
		BotToken: "12345:token",
	}
	if err := saveTelegramConfig(ctx, db, saved); err != nil {
		t.Fatal(err)
	}

	var loaded config.Config
	if err := applyStoredTelegramConfig(ctx, db, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.AuthMode != config.AuthBot || loaded.APIID != saved.APIID || loaded.APIHash != saved.APIHash || loaded.BotToken != saved.BotToken {
		t.Fatalf("loaded config = %+v", loaded)
	}
}
