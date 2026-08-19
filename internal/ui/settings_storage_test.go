package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/settings"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// storageTestApp is a settings panel with no database behind it: settings.Save is a no-op without one,
// so everything about carrying values through the form is still exercised.
func storageTestApp(t *testing.T) (*App, *settingsOverlay) {
	t.Helper()
	app := newSuggestionTestApp()
	app.settings = settings.Settings{
		Locale:            "zh",
		InlineAnim:        true,
		OutgoingLayout:    "im",
		JumpToFirstUnread: true,
		RetentionDays:     60,
		BackfillDays:      7,
	}
	overlay := &settingsOverlay{
		list:    tview.NewList(),
		content: tview.NewFlex(),
		status:  tview.NewTextView(),
	}
	overlay.root = tview.NewFlex().AddItem(overlay.content, 0, 1, true)
	app.settingsOverlay = overlay
	return app, overlay
}

func TestAcceptDayCountTakesDigitsOnly(t *testing.T) {
	for _, text := range []string{"", "0", "7", "365", "99999"} {
		if !acceptDayCount(text, 0) {
			t.Errorf("acceptDayCount(%q) = false, want a day count to be accepted", text)
		}
	}
	for _, text := range []string{"-1", "7d", "1.5", "abc", "123456"} {
		if acceptDayCount(text, 0) {
			t.Errorf("acceptDayCount(%q) = true, want it rejected", text)
		}
	}
}

// A cleared field means "mid-edit", not zero. Reading it as zero would silently turn retention into
// "keep everything" and the prefetch into "off" for anyone who backspaced before retyping.
func TestFormDayCountKeepsTheStoredValueWhenBlank(t *testing.T) {
	app, _ := storageTestApp(t)
	form := app.settingsStorageForm(app.settingsOverlay)

	form.GetFormItem(storageItemRetention).(*tview.InputField).SetText("")
	if got := formDayCount(form, storageItemRetention, 60); got != 60 {
		t.Fatalf("formDayCount on a blank field = %d, want the stored 60", got)
	}
	form.GetFormItem(storageItemRetention).(*tview.InputField).SetText("0")
	if got := formDayCount(form, storageItemRetention, 60); got != 0 {
		t.Fatalf("formDayCount(\"0\") = %d, want 0: keeping everything is a real choice", got)
	}
}

// The form rebuilds settings.Settings as a literal, so anything it forgets to carry across is silently
// reset. That has already bitten this codebase once, in the General section.
func TestSaveStorageSettingsKeepsEverySetting(t *testing.T) {
	app, _ := storageTestApp(t)
	form := app.settingsStorageForm(app.settingsOverlay)

	form.GetFormItem(storageItemRetention).(*tview.InputField).SetText("14")
	form.GetFormItem(storageItemBackfill).(*tview.InputField).SetText("0")
	app.saveStorageSettings(form)

	if app.settings.RetentionDays != 14 {
		t.Errorf("RetentionDays = %d, want 14", app.settings.RetentionDays)
	}
	if app.settings.BackfillDays != settings.BackfillOff {
		t.Errorf("BackfillDays = %d, want the prefetch turned off", app.settings.BackfillDays)
	}
	if app.settings.Locale != "zh" {
		t.Errorf("Locale = %q, want zh carried across", app.settings.Locale)
	}
	if !app.settings.InlineAnim {
		t.Error("InlineAnim was reset by saving a storage setting")
	}
	if app.settings.OutgoingLayout != "im" {
		t.Errorf("OutgoingLayout = %q, want im carried across", app.settings.OutgoingLayout)
	}
	if !app.settings.JumpToFirstUnread {
		t.Error("JumpToFirstUnread was reset by saving a storage setting")
	}
}

// The General section carries the storage numbers the same way, in the other direction.
func TestSaveGeneralSettingsKeepsTheStorageNumbers(t *testing.T) {
	app, overlay := storageTestApp(t)
	form := app.settingsGeneralForm(overlay)

	form.GetFormItem(1).(*tview.Checkbox).SetChecked(false)
	app.saveGeneralSettings(form, overlay)

	if app.settings.RetentionDays != 60 {
		t.Errorf("RetentionDays = %d, want 60 kept by a General save", app.settings.RetentionDays)
	}
	if app.settings.BackfillDays != 7 {
		t.Errorf("BackfillDays = %d, want 7 kept by a General save", app.settings.BackfillDays)
	}
}

// The section has three buttons, and a default tview Form silently drops the ones that do not fit —
// which is how the message action bar shipped as an empty box twice. Measured by drawing, not by
// reading tview's layout code.
func TestStorageFormRendersEveryButton(t *testing.T) {
	// 80 columns is the floor Tsumugi targets, including a bare Linux console.
	const narrowestTerminal = 80
	formWidth := narrowestTerminal - settingsCategoryWidth
	for _, locale := range []string{"en", "zh"} {
		i18n.SetLocale(locale)
		app, overlay := storageTestApp(t)
		form := app.settingsStorageForm(overlay)

		screen := tcell.NewSimulationScreen("UTF-8")
		if err := screen.Init(); err != nil {
			t.Fatal(err)
		}
		screen.SetSize(formWidth, 24)
		form.SetBorder(true)
		form.SetRect(0, 0, formWidth, 14)
		form.Draw(screen)
		// Without Show(), GetContents reads an empty front buffer and every measurement lies.
		screen.Show()

		for i, want := range []string{i18n.KeySettingsSave, i18n.KeySettingsCleanup, i18n.KeySettingsClose} {
			if _, _, w, _ := form.GetButton(i).GetRect(); w <= 0 {
				t.Errorf("locale %s: %q was dropped at a %d-column form", locale, i18n.T(want), formWidth)
			}
		}
		screen.Fini()
	}
	i18n.SetLocale("en")
}

// Which of the two confirmation texts appears is the difference between "this will delete things" and
// "this will delete nothing", so it cannot be decided inside a modal builder no test can reach.
func TestCleanupConfirmBodyMatchesTheRetentionSetting(t *testing.T) {
	deleting := cleanupConfirmBody(30)
	forever := cleanupConfirmBody(settings.RetentionForever)
	if deleting == forever {
		t.Fatal("keeping everything gets the same warning as deleting a month of history")
	}
	if !strings.Contains(deleting, "30") {
		t.Errorf("body = %q, want it to name the 30-day window", deleting)
	}
	if !strings.Contains(deleting, strconv.Itoa(telegram.RetentionKeepNewest)) {
		t.Errorf("body = %q, want it to promise the %d-message floor", deleting, telegram.RetentionKeepNewest)
	}
	// "%!d(MISSING)" is what an argument count mismatch looks like on screen.
	for _, body := range []string{deleting, forever} {
		if strings.Contains(body, "%!") {
			t.Errorf("body = %q, arguments do not match the template", body)
		}
	}
}

func TestConfirmCleanupEscDismissesTheModalNotThePanel(t *testing.T) {
	app, overlay := storageTestApp(t)
	app.confirmStorageCleanup(overlay)

	if overlay.confirm == nil {
		t.Fatal("no confirmation was shown before a cleanup")
	}
	// Esc has to reach the modal, not close the panel underneath it and leave the user wondering
	// whether the cleanup started.
	if got := app.captureSettings(keyEvent(tcell.KeyEsc)); got != nil {
		t.Fatalf("Esc returned %v, want it consumed by the confirmation", got)
	}
	if overlay.confirm != nil {
		t.Fatal("Esc left the confirmation up")
	}
	if app.settingsOverlay == nil {
		t.Fatal("Esc closed the whole settings panel instead of the confirmation")
	}
}

func TestRequestStorageCleanupSendsTheCommand(t *testing.T) {
	app, _ := storageTestApp(t)
	cmds := make(chan telegram.Command, 2)
	app.commands = cmds

	app.requestStorageCleanup()

	select {
	case cmd := <-cmds:
		if cmd.Kind != telegram.CommandCleanupStorage {
			t.Fatalf("command = %q, want %q", cmd.Kind, telegram.CommandCleanupStorage)
		}
	default:
		t.Fatal("no command was sent")
	}
}

// The cleanup is started from an overlay that covers the status bar, so its progress has to land in the
// panel. Reporting only to the status bar would mean minutes of silence.
func TestStorageEventsReportIntoTheSettingsPanel(t *testing.T) {
	app, overlay := storageTestApp(t)
	overlay.form = app.settingsStorageForm(overlay)

	app.applyStorageEvent(telegram.Event{
		Kind:         telegram.EventStorage,
		StatusMsg:    i18n.M(i18n.KeyStatusCleanupDone, 1000, "3.7 GB", "1.1 GB"),
		StorageBytes: 1181116006,
	})

	if got := overlay.status.GetText(true); got == "" || got == i18n.T(i18n.KeySettingsHint) {
		t.Fatalf("settings status = %q, want the cleanup result", got)
	}
	if app.storageBytes != 1181116006 {
		t.Fatalf("storageBytes = %d, want the size from the event", app.storageBytes)
	}
	if got := overlay.sizeView.GetText(true); got != "1.1 GB" {
		t.Fatalf("size field = %q, want 1.1 GB", got)
	}
}

// A progress report carries no size. It must not blank the size the panel already knows.
func TestStorageProgressKeepsTheKnownSize(t *testing.T) {
	app, overlay := storageTestApp(t)
	overlay.form = app.settingsStorageForm(overlay)
	app.storageBytes = 4096
	app.updateStorageSizeView()

	app.applyStorageEvent(telegram.Event{
		Kind:      telegram.EventStorage,
		StatusMsg: i18n.M(i18n.KeyStatusCleanupPruning, 500),
	})

	if app.storageBytes != 4096 {
		t.Fatalf("storageBytes = %d, want the known size kept", app.storageBytes)
	}
	if got := overlay.sizeView.GetText(true); got != "4.0 KB" {
		t.Fatalf("size field = %q, want the known 4.0 KB", got)
	}
}
