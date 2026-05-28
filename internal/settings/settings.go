package settings

import (
	"context"
	"os"
	"strings"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

const (
	KeyLocale         = "locale"
	KeyInlineAnim     = "inline_anim"
	KeyOutgoingLayout = "outgoing_layout"
)

type Settings struct {
	Locale         string
	InlineAnim     bool
	OutgoingLayout string
}

func Defaults() Settings {
	return Settings{Locale: "en", InlineAnim: false, OutgoingLayout: "transcript"}
}

func Load(ctx context.Context, db *storage.DB) Settings {
	out := Defaults()
	localeFromDB := false
	inlineFromDB := false
	if db != nil {
		if v, ok, err := db.GetSetting(ctx, KeyLocale); err == nil && ok && strings.TrimSpace(v) != "" {
			out.Locale = strings.TrimSpace(v)
			localeFromDB = true
		}
		if v, ok, err := db.GetSetting(ctx, KeyInlineAnim); err == nil && ok {
			out.InlineAnim = v == "1" || strings.EqualFold(v, "true")
			inlineFromDB = true
		}
		if v, ok, err := db.GetSetting(ctx, KeyOutgoingLayout); err == nil && ok && strings.TrimSpace(v) != "" {
			out.OutgoingLayout = strings.TrimSpace(v)
		}
	}
	if !localeFromDB {
		if env := strings.TrimSpace(os.Getenv("TSUMUGI_LOCALE")); env != "" {
			out.Locale = env
		}
	}
	if !inlineFromDB {
		if os.Getenv("TSUMUGI_INLINE_ANIM") == "1" {
			out.InlineAnim = true
		}
	}
	if env := strings.TrimSpace(os.Getenv("TSUMUGI_OUTGOING_LAYOUT")); env != "" {
		out.OutgoingLayout = env
	}
	if out.Locale == "" {
		out.Locale = "en"
	}
	i18n.SetLocale(out.Locale)
	return out
}

func (s Settings) Save(ctx context.Context, db *storage.DB) error {
	if db == nil {
		return nil
	}
	if err := db.SetSetting(ctx, KeyLocale, s.Locale); err != nil {
		return err
	}
	inline := "0"
	if s.InlineAnim {
		inline = "1"
	}
	if err := db.SetSetting(ctx, KeyInlineAnim, inline); err != nil {
		return err
	}
	layout := s.OutgoingLayout
	if layout == "" {
		layout = "transcript"
	}
	if err := db.SetSetting(ctx, KeyOutgoingLayout, layout); err != nil {
		return err
	}
	i18n.SetLocale(s.Locale)
	return nil
}
