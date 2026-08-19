package ui

import (
	"context"
	"strconv"
	"strings"

	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/settings"
	"github.com/nemo/Tsumugi/internal/storage"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// Form item indices in the storage section. Named because tview forms are addressed positionally,
// and a mis-numbered GetFormItem reads the wrong field into the wrong setting.
const (
	storageItemRetention = 0
	storageItemBackfill  = 1
	storageItemSize      = 2
)

// settingsStorageForm holds the two numbers that decide how large the database gets, plus the button
// that applies them to what is already stored.
//
// Retention lived in General and moved here: it belongs next to the prefetch depth it constrains and
// next to the size it explains, and a "clean up now" button is meaningless anywhere else.
func (a *App) settingsStorageForm(overlay *settingsOverlay) *tview.Form {
	form := tview.NewForm()
	form.AddInputField(i18n.T(i18n.KeySettingsRetention), strconv.Itoa(a.settings.RetentionDays), 6, acceptDayCount, nil).
		AddInputField(i18n.T(i18n.KeySettingsBackfill), strconv.Itoa(a.settings.BackfillDays), 6, acceptDayCount, nil).
		AddTextView(i18n.T(i18n.KeySettingsDBSize), a.storageSizeText(), 24, 1, false, false).
		AddButton(i18n.T(i18n.KeySettingsSave), func() {
			a.saveStorageSettings(form)
		}).
		AddButton(i18n.T(i18n.KeySettingsCleanup), func() {
			a.confirmStorageCleanup(overlay)
		}).
		AddButton(i18n.T(i18n.KeySettingsClose), func() {
			a.closeSettings()
		})
	if view, ok := form.GetFormItem(storageItemSize).(*tview.TextView); ok {
		overlay.sizeView = view
	}
	// Asked for off the UI thread. It is only two PRAGMAs, but the database has a single connection
	// and a compaction can hold it for minutes, so reading it inline would freeze the whole UI at
	// exactly the moment the user is watching this panel.
	a.refreshStorageSize()
	return form
}

// acceptDayCount restricts a field to a day count. Zero is a legitimate value in both fields, so
// there is no minimum to enforce here — only "digits, and not absurdly many".
func acceptDayCount(text string, _ rune) bool {
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(text) <= 5
}

func (a *App) saveStorageSettings(form *tview.Form) {
	// Rebuilt as a literal, so every field has to be carried across explicitly: omitting one
	// silently resets a setting the user changed somewhere else.
	next := settings.Settings{
		Locale:            a.settings.Locale,
		InlineAnim:        a.settings.InlineAnim,
		OutgoingLayout:    a.settings.OutgoingLayout,
		JumpToFirstUnread: a.settings.JumpToFirstUnread,
		RetentionDays:     formDayCount(form, storageItemRetention, a.settings.RetentionDays),
		BackfillDays:      formDayCount(form, storageItemBackfill, a.settings.BackfillDays),
	}
	if err := next.Save(context.Background(), a.db); err != nil {
		a.setSettingsStatus("[red]" + err.Error())
		return
	}
	a.settings = next
	a.setSettingsStatus(i18n.T(i18n.KeySettingsSaved))
}

// formDayCount reads a day count out of the form, keeping the stored value when the field is blank.
//
// A cleared field means "I am mid-edit", not "zero": treating it as zero would turn an empty
// retention box into "keep everything" and an empty prefetch box into "prefetch nothing", neither of
// which anyone asked for.
func formDayCount(form *tview.Form, index, fallback int) int {
	field, ok := form.GetFormItem(index).(*tview.InputField)
	if !ok {
		return fallback
	}
	raw := strings.TrimSpace(field.GetText())
	if raw == "" {
		return fallback
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 0 {
		return fallback
	}
	return days
}

// cleanupConfirmBody is what the user reads before agreeing to a cleanup.
//
// Two texts, because with retention set to forever nothing is deleted at all and warning about
// deletion would be a lie. Pure, so the choice between them is testable.
func cleanupConfirmBody(retentionDays int) string {
	if retentionDays == settings.RetentionForever {
		return i18n.T(i18n.KeySettingsCleanupForever)
	}
	return i18n.Tf(i18n.KeySettingsCleanupBody, retentionDays, telegram.RetentionKeepNewest)
}

// confirmStorageCleanup asks before deleting, because the numbers on this panel are easy to mistype
// and the operation is long enough that stopping it halfway is not really an option.
func (a *App) confirmStorageCleanup(overlay *settingsOverlay) {
	modal := tview.NewModal().
		SetText(cleanupConfirmBody(a.settings.RetentionDays)).
		AddButtons([]string{i18n.T(i18n.KeySettingsCleanupConfirm), i18n.T(i18n.KeyActionCancel)}).
		SetDoneFunc(func(_ int, label string) {
			confirmed := label == i18n.T(i18n.KeySettingsCleanupConfirm)
			a.dismissStorageConfirm()
			if confirmed {
				a.requestStorageCleanup()
			}
		})
	// Kept on the overlay rather than replacing it, so finishing here returns to the panel the
	// cleanup was started from instead of dropping the user back into the chat list.
	overlay.confirm = modal
	a.app.SetRoot(modal, true)
	a.app.SetFocus(modal)
}

func (a *App) dismissStorageConfirm() bool {
	overlay := a.settingsOverlay
	if overlay == nil || overlay.confirm == nil {
		return false
	}
	overlay.confirm = nil
	a.app.SetRoot(overlay.root, true)
	if overlay.form != nil {
		a.app.SetFocus(overlay.form)
	} else {
		a.app.SetFocus(overlay.list)
	}
	return true
}

func (a *App) requestStorageCleanup() {
	if a.commands == nil {
		return
	}
	a.setSettingsStatus(i18n.T(i18n.KeyStatusCleanupStarted))
	a.commands <- telegram.Command{Kind: telegram.CommandCleanupStorage}
}

// applyStorageEvent reports housekeeping into the settings panel.
//
// The panel is the only place this can go while it is open: it covers the status bar, so a cleanup
// that reported only there would run for minutes with nothing on screen. When it is not open the
// status bar is exactly right, which is what the fallback in setSettingsStatus does.
func (a *App) applyStorageEvent(event telegram.Event) {
	if event.StorageBytes > 0 {
		a.storageBytes = event.StorageBytes
		a.updateStorageSizeView()
	}
	if event.Error != nil {
		// Already on the status bar from applyEvent; mirrored here because that bar is hidden.
		a.setSettingsStatus("[red]" + event.Error.Error())
		return
	}
	if event.StatusMsg.IsZero() {
		return
	}
	if a.settingsOverlay != nil {
		a.setSettingsStatus(event.StatusMsg.String())
		return
	}
	a.setStatusFrom(event.StatusMsg)
}

// refreshStorageSize reads the database size in the background and shows it when it arrives.
func (a *App) refreshStorageSize() {
	db := a.db
	if db == nil {
		return
	}
	go func() {
		bytes, err := db.DatabaseFileBytes(context.Background())
		if err != nil {
			return
		}
		a.app.QueueUpdateDraw(func() {
			a.storageBytes = bytes
			a.updateStorageSizeView()
		})
	}()
}

func (a *App) updateStorageSizeView() {
	if a.settingsOverlay == nil || a.settingsOverlay.sizeView == nil {
		return
	}
	a.settingsOverlay.sizeView.SetText(a.storageSizeText())
}

func (a *App) storageSizeText() string {
	if a.storageBytes <= 0 {
		return "…"
	}
	return storage.FormatBytes(a.storageBytes)
}
