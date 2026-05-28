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

type settingsOverlay struct {
	root    *tview.Flex
	list    *tview.List
	content *tview.Flex
	status  *tview.TextView
	form    *tview.Form
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

	openGeneral := func() {
		form := a.settingsGeneralForm(overlay)
		showSection(i18n.T(i18n.KeySettingsGeneral), form, form, true)
	}

	list.AddItem(i18n.T(i18n.KeySettingsGeneral), i18n.T(i18n.KeySettingsGeneralDesc), 0, openGeneral)
	list.AddItem(i18n.T(i18n.KeySettingsNetwork), i18n.T(i18n.KeySettingsNetworkDesc), 0, func() {
		a.settingsOverlay = nil
		a.showProxySettings()
	})

	hint := tview.NewTextView().
		SetDynamicColors(true).
		SetText(i18n.T(i18n.KeySettingsHint))
	showSection(i18n.T(i18n.KeySettingsCategories), hint, nil, false)

	list.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeySettingsCategories) + " ")
	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(content, 0, 1, true).
		AddItem(status, 3, 0, false)
	view := tview.NewFlex().
		AddItem(list, 28, 0, true).
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
		AddButton(i18n.T(i18n.KeySettingsSave), func() {
			dropdown := form.GetFormItem(0).(*tview.DropDown)
			_, localeText := dropdown.GetCurrentOption()
			locale := "en"
			if localeText == "中文 (zh)" {
				locale = "zh"
			}
			inlineAnim := form.GetFormItem(1).(*tview.Checkbox).IsChecked()
			next := settings.Settings{
				Locale:         locale,
				InlineAnim:     inlineAnim,
				OutgoingLayout: a.settings.OutgoingLayout,
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
		}).
		AddButton(i18n.T(i18n.KeySettingsClose), func() {
			a.closeSettings()
		})
	return form
}

func (a *App) refreshSettingsUI(overlay *settingsOverlay) {
	if overlay == nil || overlay.list == nil {
		return
	}
	idx := overlay.list.GetCurrentItem()
	overlay.list.Clear()
	overlay.list.AddItem(i18n.T(i18n.KeySettingsGeneral), i18n.T(i18n.KeySettingsGeneralDesc), 0, func() {
		form := a.settingsGeneralForm(overlay)
		overlay.content.Clear()
		overlay.form = form
		form.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeySettingsGeneral) + " ")
		overlay.content.AddItem(form, 0, 1, true)
		a.app.SetFocus(form)
	})
	overlay.list.AddItem(i18n.T(i18n.KeySettingsNetwork), i18n.T(i18n.KeySettingsNetworkDesc), 0, func() {
		a.settingsOverlay = nil
		a.showProxySettings()
	})
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
		a.closeSettings()
		return nil
	case tcell.KeyTAB:
		if focus == overlay.list && overlay.form != nil {
			a.app.SetFocus(overlay.form)
			return nil
		}
		return event
	case tcell.KeyBacktab:
		if focus == overlay.form {
			a.app.SetFocus(overlay.list)
			return nil
		}
		return event
	case tcell.KeyRight:
		if focus == overlay.list && overlay.form != nil {
			a.app.SetFocus(overlay.form)
			return nil
		}
	case tcell.KeyLeft:
		if focus == overlay.form {
			a.app.SetFocus(overlay.list)
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
