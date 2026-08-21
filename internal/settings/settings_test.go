package settings

import (
	"bytes"
	"context"
	"testing"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/storage"
)

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	db, _, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db.SetCipher(cipher)
	return db
}

func TestSettingsSaveAndLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	loaded := Load(ctx, db)
	if loaded.Locale != "en" || loaded.InlineAnim {
		t.Fatalf("defaults = %+v", loaded)
	}

	// Notify has to be spelled out: an empty value normalizes to the default, which is exactly the
	// behaviour that keeps a typo in a shell profile from silencing notifications.
	want := Settings{Locale: "zh", InlineAnim: true, OutgoingLayout: "im", Notify: DefaultNotify}
	if err := want.Save(ctx, db); err != nil {
		t.Fatal(err)
	}
	got := Load(ctx, db)
	if got != want {
		t.Fatalf("loaded = %+v, want %+v", got, want)
	}
	if i18n.Locale() != "zh" {
		t.Fatalf("locale = %q, want zh", i18n.Locale())
	}
}

func TestSettingsEnvFallbackWhenDBEmpty(t *testing.T) {
	t.Setenv("TSUMUGI_LOCALE", "zh")
	t.Setenv("TSUMUGI_INLINE_ANIM", "1")

	got := Load(context.Background(), testDB(t))
	if got.Locale != "zh" || !got.InlineAnim {
		t.Fatalf("env fallback = %+v", got)
	}
}

func TestSettingsDBOverridesEnv(t *testing.T) {
	t.Setenv("TSUMUGI_LOCALE", "zh")
	t.Setenv("TSUMUGI_INLINE_ANIM", "1")

	ctx := context.Background()
	db := testDB(t)
	s := Settings{Locale: "en", InlineAnim: false}
	if err := s.Save(ctx, db); err != nil {
		t.Fatal(err)
	}
	got := Load(ctx, db)
	if got.Locale != "en" || got.InlineAnim {
		t.Fatalf("db should override env: %+v", got)
	}
}
