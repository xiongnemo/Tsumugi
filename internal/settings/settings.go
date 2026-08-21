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
	// KeyBackfillDays is how deep the background prefetch reaches. Zero turns it off.
	KeyBackfillDays = "backfill_days"
	// KeyNotify is how an arriving message announces itself.
	KeyNotify = "notify"
)

// How an incoming message announces itself.
//
// The bell is the default because it is the only mechanism a bare Linux console has, and it needs no
// capability guessing. The desktop option is opt-in rather than detected: a terminal that does not
// implement the escape sequence prints it as garbage into the middle of the interface, and
// TERM_PROGRAM sniffing is not reliable enough to bet the display on.
const (
	NotifyOff     = "off"
	NotifyBell    = "bell"
	NotifyDesktop = "bell+desktop"
)

// DefaultNotify is the terminal bell.
const DefaultNotify = NotifyBell

// DefaultRetentionDays is how much history is kept when nothing has been chosen.
//
// Sixty rather than thirty so it sits comfortably deeper than the default backfill horizon: if
// retention cut at the same depth the backfill reaches, the two would fight, deleting and
// re-downloading the same messages forever.
const DefaultRetentionDays = 60

// RetentionForever is the value that disables pruning entirely.
const RetentionForever = 0

// DefaultBackfillDays is how far back the background prefetch reaches by default.
//
// The mainstream clients prefetch nothing at all: TDLib's message database is a cache filled as a
// side effect of what you actually open, which is why their databases stay small. Prefetching is
// kept, but small, because scrolling up into a cache miss is a visible wait — and it is a setting
// rather than a constant because how much offline history is worth the disk is the user's call, not
// ours.
const DefaultBackfillDays = 7

// BackfillOff turns the prefetch off entirely, leaving history to load on demand.
const BackfillOff = 0

type Settings struct {
	Locale            string
	InlineAnim        bool
	OutgoingLayout    string
	JumpToFirstUnread bool
	// RetentionDays is how many days of history to keep; RetentionForever keeps all of it.
	RetentionDays int
	// BackfillDays is how many days back the background prefetch reaches; BackfillOff disables it.
	BackfillDays int
	// Notify is one of NotifyOff, NotifyBell or NotifyDesktop.
	Notify string
}

func Defaults() Settings {
	return Settings{
		Locale:            "en",
		InlineAnim:        false,
		OutgoingLayout:    "transcript",
		JumpToFirstUnread: true,
		RetentionDays:     DefaultRetentionDays,
		BackfillDays:      DefaultBackfillDays,
		Notify:            DefaultNotify,
	}
}

// daysSetting reads a day count, preferring what the user stored over the environment.
//
// Zero is a meaningful value for both day counts — "keep everything" and "prefetch nothing" — so it
// has to survive as a stored choice. Only a negative or unparseable value falls through to the next
// source.
func daysSetting(ctx context.Context, db *storage.DB, key, env string, fallback int) int {
	if db != nil {
		if v, ok, err := db.GetSetting(ctx, key); err == nil && ok {
			if days, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && days >= 0 {
				return days
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv(env)); raw != "" {
		if days, err := strconv.Atoi(raw); err == nil && days >= 0 {
			return days
		}
	}
	return fallback
}

func Load(ctx context.Context, db *storage.DB) Settings {
	out := Defaults()
	localeFromDB := false
	inlineFromDB := false
	jumpFromDB := false
	notifyFromDB := false
	out.RetentionDays = daysSetting(ctx, db, KeyRetentionDays, "TSUMUGI_RETENTION_DAYS", DefaultRetentionDays)
	out.BackfillDays = daysSetting(ctx, db, KeyBackfillDays, "TSUMUGI_BACKFILL_DAYS", DefaultBackfillDays)
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
		if v, ok, err := db.GetSetting(ctx, KeyNotify); err == nil && ok && strings.TrimSpace(v) != "" {
			out.Notify = strings.TrimSpace(v)
			notifyFromDB = true
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
	if !notifyFromDB {
		if env := strings.TrimSpace(os.Getenv("TSUMUGI_NOTIFY")); env != "" {
			out.Notify = env
		}
	}
	out.Notify = normalizeNotify(out.Notify)
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

// normalizeNotify keeps an unknown or empty value from silently disabling notifications: a typo in a
// shell profile should fall back to the default, not to silence.
func normalizeNotify(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case NotifyOff:
		return NotifyOff
	case NotifyDesktop:
		return NotifyDesktop
	default:
		return NotifyBell
	}
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
	if err := db.SetSetting(ctx, KeyBackfillDays, strconv.Itoa(s.BackfillDays)); err != nil {
		return err
	}
	if err := db.SetSetting(ctx, KeyNotify, normalizeNotify(s.Notify)); err != nil {
		return err
	}
	i18n.SetLocale(s.Locale)
	return nil
}
