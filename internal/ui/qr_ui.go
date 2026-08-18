package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// showQRPrompt displays a scannable login code.
//
// Re-rendered in place on every refresh. QR.Auth re-invokes its show callback each time the token
// expires, and calling SetRoot again per refresh would stack roots and steal focus, so the
// primitives are built once and only their content is replaced afterwards.
func (a *App) showQRPrompt(prompt *telegram.AuthPrompt) {
	if a.qrView != nil {
		a.updateQRPrompt(prompt)
		return
	}

	// SetWrap(false) is mandatory: wrapping folds the code in half and makes it unscannable.
	view := tview.NewTextView().SetDynamicColors(true).SetWrap(false).SetTextAlign(tview.AlignCenter)
	url := tview.NewTextView().SetWrap(true).SetTextAlign(tview.AlignCenter)
	help := tview.NewTextView().SetWordWrap(true).SetTextAlign(tview.AlignCenter)
	a.qrView = view
	a.qrURLView = url
	a.qrPrompt = prompt

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(help, 3, 0, false).
		AddItem(view, 0, 1, true).
		AddItem(url, 2, 0, false).
		AddItem(tview.NewTextView().SetTextAlign(tview.AlignCenter).
			SetText(i18n.T(i18n.KeyAuthQRUsePhone)+" (p) · "+i18n.T(i18n.KeyAuthQRCopiedURL)+" (y)"), 1, 0, false)
	layout.SetBorder(true).SetTitle(" " + i18n.T(prompt.TitleKey) + " ")

	// The overlay is keyboard-only: a bare console has no mouse, so every action needs a key.
	layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'y':
			a.copyQRURL()
			return nil
		case 'p':
			a.fallBackToPhoneLogin()
			return nil
		}
		return event
	})

	a.updateQRPrompt(prompt)
	a.app.SetRoot(layout, true)
	a.app.SetFocus(layout)
}

// updateQRPrompt swaps in a refreshed code without rebuilding the overlay.
func (a *App) updateQRPrompt(prompt *telegram.AuthPrompt) {
	a.qrPrompt = prompt
	if a.qrView != nil {
		a.qrView.SetText(prompt.QRPreview)
	}
	if a.qrURLView != nil {
		a.qrURLView.SetText(prompt.QRURL)
	}
}

func (a *App) closeQRPrompt() {
	a.qrView = nil
	a.qrURLView = nil
	a.qrPrompt = nil
}

// copyQRURL puts the login URL on the clipboard, for terminals that render the code badly.
func (a *App) copyQRURL() {
	if a.qrPrompt == nil || a.qrPrompt.QRURL == "" {
		return
	}
	if err := copyText(a.qrPrompt.QRURL); err != nil {
		a.setStatusError(err)
		return
	}
	a.setStatusMsg(i18n.KeyAuthQRCopiedURL)
}

// fallBackToPhoneLogin abandons the QR flow.
//
// Reuses the logout path rather than adding a control event of its own: that already clears the
// stored login method and session and returns to the wizard, which is exactly what "I would rather
// type my number" means. Restarting the auth flow in place is not possible — the method is chosen
// before client.Run, because the login-token handler has to be registered on the dispatcher first.
func (a *App) fallBackToPhoneLogin() {
	a.closeQRPrompt()
	a.requestLogout()
}
