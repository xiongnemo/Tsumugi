package settings

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

const (
	KeyLocale         = "locale"
	KeyInlineAnim     = "inline_anim"
	KeyOutgoingLayout = "outgoing_layout"
	// KeyJumpToFirstUnread controls whether opening a chat lands on the first unread message
	// instead of the newest one.
	KeyJumpToFirstUnread = "jump_to_first_unread"
	// KeyRetentionDays is how many days of message history to keep on disk. Zero keeps
	// everything.
	KeyRetentionDays = "retention_days"
)

// DefaultRetentionDays is how much history is kept when nothing has been chosen.
//
// Sixty rather than thirty so it sits comfortably deeper than the default backfill horizon: if
// retention cut at the same depth the backfill reaches, the two would fight, deleting and
// re-downloading the same messages forever.
const DefaultRetentionDays = 60

// RetentionForever is the value that disables pruning entirely.
const RetentionForever = 0

type Settings struct {
	Locale            string
	InlineAnim        bool
	OutgoingLayout    string
	JumpToFirstUnread bool
	// RetentionDays is how many days of history to keep; RetentionForever keeps all of it.
	RetentionDays int
}

func Defaults() Settings {
	return Settings{
		Locale:            "en",
		InlineAnim:        false,
		OutgoingLayout:    "transcript",
		JumpToFirstUnread: true,
		RetentionDays:     DefaultRetentionDays,
	}
}

func Load(ctx context.Context, db *storage.DB) Settings {
	out := Defaults()
	localeFromDB := false
	inlineFromDB := false
	jumpFromDB := false
	retentionFromDB := false
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
		if v, ok, err := db.GetSetting(ctx, KeyJumpToFirstUnread); err == nil && ok {
			out.JumpToFirstUnread = v == "1" || strings.EqualFold(v, "true")
			jumpFromDB = true
		}
		if v, ok, err := db.GetSetting(ctx, KeyRetentionDays); err == nil && ok {
			if days, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && days >= 0 {
				out.RetentionDays = days
				retentionFromDB = true
			}
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
	if !retentionFromDB {
		if env := strings.TrimSpace(os.Getenv("TSUMUGI_RETENTION_DAYS")); env != "" {
			if days, err := strconv.Atoi(env); err == nil && days >= 0 {
				out.RetentionDays = days
			}
		}
	}
	// Default is on, so the env override has to be able to turn it off as well as on.
	if !jumpFromDB {
		if env := strings.TrimSpace(os.Getenv("TSUMUGI_JUMP_UNREAD")); env != "" {
			out.JumpToFirstUnread = env == "1" || strings.EqualFold(env, "true")
		}
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
	jump := "0"
	if s.JumpToFirstUnread {
		jump = "1"
	}
	if err := db.SetSetting(ctx, KeyJumpToFirstUnread, jump); err != nil {
		return err
	}
	if err := db.SetSetting(ctx, KeyRetentionDays, strconv.Itoa(s.RetentionDays)); err != nil {
		return err
	}
	i18n.SetLocale(s.Locale)
	return nil
}
