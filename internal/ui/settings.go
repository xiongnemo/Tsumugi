package ui

import (
	"context"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/settings"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// settingsCategoryWidth is the fixed width of the category rail, so the section forms get whatever
// the terminal has left. Named because a section with several buttons only renders all of them above
// a certain form width — see TestStorageFormRendersEveryButton.
const settingsCategoryWidth = 28

type settingsOverlay struct {
	root    *tview.Flex
	list    *tview.List
	content *tview.Flex
	status  *tview.TextView
	form    *tview.Form
	// sizeView is the database-size field of the storage section, updated as a cleanup progresses.
	sizeView *tview.TextView
	// confirm is a modal shown over this overlay. Tracked so Esc dismisses the modal rather than
	// the whole settings panel underneath it.
	confirm tview.Primitive
}

func (a *App) showSettings() {
	if a.settingsOverlay != nil {
		a.app.SetFocus(a.settingsOverlay.list)
		return
	}

	overlay := &settingsOverlay{}
	list := tview.NewList().ShowSecondaryText(true)
	content := tview.NewFlex().SetDirection(tview.FlexRow)
	status := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetWrap(false)
	status.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeySettingsStatus) + " ")
	status.SetText(i18n.T(i18n.KeySettingsHint))

	overlay.list = list
	overlay.content = content
	overlay.status = status

	showSection := func(title string, panel tview.Primitive, form *tview.Form, focusForm bool) {
		overlay.content.Clear()
		overlay.form = form
		if form != nil {
			form.SetBorder(true).SetTitle(" " + title + " ")
			overlay.content.AddItem(form, 0, 1, true)
		} else if tv, ok := panel.(*tview.TextView); ok {
			tv.SetBorder(true).SetTitle(" " + title + " ")
			overlay.content.AddItem(tv, 0, 1, true)
		}
		if focusForm && form != nil {
			a.app.SetFocus(form)
		}
	}

	a.populateSettingsList(overlay)

	hint := tview.NewTextView().
		SetDynamicColors(true).
		SetText(i18n.T(i18n.KeySettingsHint))
	showSection(i18n.T(i18n.KeySettingsCategories), hint, nil, false)

	list.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeySettingsCategories) + " ")
	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(content, 0, 1, true).
		AddItem(status, 3, 0, false)
	view := tview.NewFlex().
		AddItem(list, settingsCategoryWidth, 0, true).
		AddItem(right, 0, 1, true)
	overlay.root = view
	a.settingsOverlay = overlay

	a.app.SetRoot(view, true)
	a.app.SetFocus(list)
}

func (a *App) settingsGeneralForm(overlay *settingsOverlay) *tview.Form {
	form := tview.NewForm()
	localeIndex := 0
	if a.settings.Locale == "zh" {
		localeIndex = 1
	}

	form.AddDropDown(i18n.T(i18n.KeySettingsLanguage), []string{"English (en)", "中文 (zh)"}, localeIndex, nil).
		AddCheckbox(i18n.T(i18n.KeySettingsInlineAnim), a.settings.InlineAnim, nil).
		AddCheckbox(i18n.T(i18n.KeySettingsJumpUnread), a.settings.JumpToFirstUnread, nil).
		AddButton(i18n.T(i18n.KeySettingsSave), func() {
			a.saveGeneralSettings(form, overlay)
		}).
		AddButton(i18n.T(i18n.KeySettingsClose), func() {
			a.closeSettings()
		})
	return form
}

// saveGeneralSettings applies the General section. A method rather than the button closure it used to
// be, so a test can run the same code the button runs.
func (a *App) saveGeneralSettings(form *tview.Form, overlay *settingsOverlay) {
	dropdown := form.GetFormItem(0).(*tview.DropDown)
	_, localeText := dropdown.GetCurrentOption()
	locale := "en"
	if localeText == "中文 (zh)" {
		locale = "zh"
	}
	inlineAnim := form.GetFormItem(1).(*tview.Checkbox).IsChecked()
	jumpUnread := form.GetFormItem(2).(*tview.Checkbox).IsChecked()
	// Rebuilt as a literal, so every field has to be carried across explicitly: omitting one
	// silently resets the user's choice whenever they touch any other General setting. The storage
	// day counts live in their own section and are carried through untouched here.
	next := settings.Settings{
		Locale:            locale,
		InlineAnim:        inlineAnim,
		OutgoingLayout:    a.settings.OutgoingLayout,
		JumpToFirstUnread: jumpUnread,
		RetentionDays:     a.settings.RetentionDays,
		BackfillDays:      a.settings.BackfillDays,
	}
	if err := next.Save(context.Background(), a.db); err != nil {
		a.setSettingsStatus("[red]" + err.Error())
		return
	}
	prevLocale := a.settings.Locale
	a.settings = next
	a.messages.SetInlineAnim(next.InlineAnim)
	a.messages.SetLayoutMode(render.ParseLayoutMode(next.OutgoingLayout))
	a.syncInlineAnimTicker()
	if next.InlineAnim {
		a.reloadCurrentChatForInlineAnim()
	}
	if prevLocale != next.Locale {
		a.relocalizeVisibleMessages()
		a.applyMainLocale()
		a.refreshSettingsUI(overlay)
		return
	}
	a.setSettingsStatus(i18n.T(i18n.KeySettingsSaved))
}

func (a *App) settingsAccountForm(overlay *settingsOverlay) *tview.Form {
	_ = overlay
	form := tview.NewForm().
		AddTextView(i18n.T(i18n.KeySettingsAccountMode), string(a.cfg.AuthMode), 40, 1, false, false).
		AddTextView(i18n.T(i18n.KeySettingsAccountCache), i18n.T(i18n.KeyLogoutKeepsCache), 48, 3, true, false).
		AddButton(i18n.T(i18n.KeyLogoutButton), func() {
			a.showLogoutConfirm()
		}).
		AddButton(i18n.T(i18n.KeySettingsClose), func() {
			a.closeSettings()
		})
	return form
}

// populateSettingsList fills the category list.
//
// One copy, used by both the first build and the rebuild after a locale change. It was two, which
// meant a new category had to be added in both places to exist in both — exactly the kind of
// divergence that ships a section you can only reach before switching languages.
func (a *App) populateSettingsList(overlay *settingsOverlay) {
	open := func(titleKey string, build func(*settingsOverlay) *tview.Form) func() {
		return func() {
			form := build(overlay)
			overlay.content.Clear()
			overlay.form = form
			form.SetBorder(true).SetTitle(" " + i18n.T(titleKey) + " ")
			overlay.content.AddItem(form, 0, 1, true)
			a.app.SetFocus(form)
		}
	}
	overlay.list.Clear()
	overlay.list.AddItem(i18n.T(i18n.KeySettingsGeneral), i18n.T(i18n.KeySettingsGeneralDesc), 0,
		open(i18n.KeySettingsGeneral, a.settingsGeneralForm))
	overlay.list.AddItem(i18n.T(i18n.KeySettingsStorage), i18n.T(i18n.KeySettingsStorageDesc), 0,
		open(i18n.KeySettingsStorage, a.settingsStorageForm))
	overlay.list.AddItem(i18n.T(i18n.KeySettingsAccount), i18n.T(i18n.KeySettingsAccountDesc), 0,
		open(i18n.KeySettingsAccount, a.settingsAccountForm))
	overlay.list.AddItem(i18n.T(i18n.KeySettingsNetwork), i18n.T(i18n.KeySettingsNetworkDesc), 0, func() {
		// The proxy panel is its own overlay with its own root, not a form in this one.
		a.settingsOverlay = nil
		a.showProxySettings()
	})
}

func (a *App) refreshSettingsUI(overlay *settingsOverlay) {
	if overlay == nil || overlay.list == nil {
		return
	}
	idx := overlay.list.GetCurrentItem()
	a.populateSettingsList(overlay)
	if idx >= 0 && idx < overlay.list.GetItemCount() {
		overlay.list.SetCurrentItem(idx)
	}
	overlay.status.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeySettingsStatus) + " ")
	a.setSettingsStatus(i18n.T(i18n.KeySettingsSaved))
	a.app.SetFocus(overlay.form)
}

func (a *App) setSettingsStatus(text string) {
	if a.settingsOverlay == nil || a.settingsOverlay.status == nil {
		a.setStatus(text)
		return
	}
	a.settingsOverlay.status.SetText(text)
}

func (a *App) closeSettings() {
	a.settingsOverlay = nil
	a.app.SetRoot(a.root, true)
	if a.currentChat != "" {
		a.app.SetFocus(a.messages)
	} else {
		a.app.SetFocus(a.chats)
	}
	a.updateFocusStyle()
}

func (a *App) showLogoutConfirm() {
	a.settingsOverlay = nil
	modal := tview.NewModal().
		SetText(i18n.T(i18n.KeyLogoutConfirmBody)).
		AddButtons([]string{i18n.T(i18n.KeyLogoutConfirm), i18n.T(i18n.KeyActionCancel)}).
		SetDoneFunc(func(_ int, label string) {
			if label != i18n.T(i18n.KeyLogoutConfirm) {
				a.app.SetRoot(a.root, true)
				a.app.SetFocus(a.chats)
				a.updateFocusStyle()
				return
			}
			a.requestLogout()
		})
	a.app.SetRoot(modal, true)
	a.app.SetFocus(modal)
}

func (a *App) requestLogout() {
	reply := make(chan error, 1)
	if a.control != nil {
		a.control <- ControlEvent{Kind: ControlLogout, Reply: reply}
	} else {
		reply <- nil
	}
	a.setStatusMsg(i18n.KeyLogoutStatusRunning)
	go func() {
		err := <-reply
		a.app.QueueUpdateDraw(func() {
			if err != nil {
				a.app.SetRoot(a.root, true)
				a.app.SetFocus(a.chats)
				a.setStatusError(err)
				return
			}
			a.cfg = a.cfg.WithoutTelegramAuth()
			a.resetTelegramView(i18n.T(i18n.KeyLogoutStatusDone))
			a.showOnboarding()
			a.setStatusMsg(i18n.KeyLogoutStatusDone)
		})
	}()
}

func (a *App) captureSettings(event *tcell.EventKey) *tcell.EventKey {
	if a.settingsOverlay == nil {
		return event
	}
	overlay := a.settingsOverlay
	focus := a.app.GetFocus()

	switch event.Key() {
	case tcell.KeyCtrlC:
		a.app.Stop()
		return nil
	case tcell.KeyEsc:
		// A modal on top of the panel gets Esc first; closing the panel out from under it would
		// leave the user in the chat list wondering whether the cleanup started.
		if a.dismissStorageConfirm() {
			return nil
		}
		a.closeSettings()
		return nil
	case tcell.KeyTAB:
		if focus == overlay.list && overlay.form != nil {
			a.app.SetFocus(overlay.form)
			return nil
		}
		return event
	case tcell.KeyRight:
		if focus == overlay.list && overlay.form != nil {
			a.app.SetFocus(overlay.form)
			return nil
		}
	}

	if focus == overlay.form {
		return event
	}
	if focus == overlay.list {
		switch event.Rune() {
		case 'q':
			a.app.Stop()
			return nil
		}
		return event
	}
	return event
}

func (a *App) relocalizeVisibleMessages() {
	msgs := a.messages.Messages()
	if len(msgs) == 0 {
		return
	}
	out := make([]telegram.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = msg
		if msg.Media.Kind != "" {
			out[i].Media = telegram.LocalizeMediaAttachment(msg.Media)
		}
	}
	a.messages.ReplaceMessagesPreserveState(out)
	a.applyMessagesPaneTitle()
	a.refreshStatusBar()
}

func (a *App) syncInlineAnimTicker() {
	a.messages.SetInlineAnim(a.settings.InlineAnim)
	if a.settings.InlineAnim {
		a.messages.ResetInlineAnimTick()
	}
	// This runs from the settings form callback, which is already on the UI
	// event path; do not queue another UI update from here.
}

func (a *App) reloadCurrentChatForInlineAnim() {
	if a.currentChat == "" || a.currentChat == "welcome" {
		return
	}
	a.commands <- telegram.Command{Kind: telegram.CommandOpenChat, PeerKey: a.currentChat}
}
