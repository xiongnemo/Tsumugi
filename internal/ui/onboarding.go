package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/config"
	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/settings"
	"github.com/nemo/Tsumugi/internal/telegram"
	"github.com/nemo/Tsumugi/internal/version"
)

func (a *App) showOnboarding() {
	a.onboardingActive = true
	draft := a.cfg
	status := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true).
		SetText(i18n.T(i18n.KeyOnboardingStatusReady))
	status.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyOnboardingStatus) + " ")

	var showWelcome func()
	var showLanguage func()
	var showAuthMode func()
	var showAPI func()
	var showIdentity func()
	var showSummary func()

	setPage := func(title, body string, form *tview.Form) {
		bodyView := tview.NewTextView().
			SetDynamicColors(true).
			SetWordWrap(true).
			SetText(body)
		bodyView.SetBorder(true).SetTitle(" " + title + " ")
		form.SetBorder(true)
		panel := tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(bodyView, 0, 1, false).
			AddItem(form, 10, 0, true).
			AddItem(status, 4, 0, false)
		centered := tview.NewFlex().
			AddItem(tview.NewBox(), 0, 1, false).
			AddItem(panel, 72, 0, true).
			AddItem(tview.NewBox(), 0, 1, false)
		centered.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			if event.Key() == tcell.KeyCtrlC {
				a.app.Stop()
				return nil
			}
			return event
		})
		a.app.SetRoot(centered, true)
		a.app.SetFocus(form)
	}

	showWelcome = func() {
		form := tview.NewForm().
			AddButton(i18n.T(i18n.KeyOnboardingStart), showLanguage).
			AddButton(i18n.T(i18n.KeyUIFooterQuit), func() { a.app.Stop() })
		setPage(i18n.T(i18n.KeyOnboardingWelcomeTitle), i18n.T(i18n.KeyOnboardingWelcomeBody)+"\n\n"+onboardingProxyLine(draft), form)
	}

	showLanguage = func() {
		locale := i18n.Locale()
		initial := 0
		if locale == "zh" {
			initial = 1
		}
		form := tview.NewForm()
		form.AddDropDown(i18n.T(i18n.KeySettingsLanguage), []string{"English (en)", "中文 (zh)"}, initial, func(_ string, index int) {
			if index == 1 {
				locale = "zh"
			} else {
				locale = "en"
			}
		}).
			AddButton(i18n.T(i18n.KeyOnboardingNext), func() {
				next := a.settings
				next.Locale = locale
				if next.Locale == "" {
					next = settings.Defaults()
				}
				if err := next.Save(context.Background(), a.db); err != nil {
					status.SetText(i18n.Tf(i18n.KeyStatusError, err.Error()))
					return
				}
				a.settings = next
				a.applyMainLocale()
				status.SetText(i18n.T(i18n.KeySettingsSaved))
				showAuthMode()
			}).
			AddButton(i18n.T(i18n.KeyOnboardingBack), showWelcome)
		setPage(i18n.T(i18n.KeyOnboardingLanguageTitle), i18n.T(i18n.KeyOnboardingLanguageBody), form)
	}

	showAuthMode = func() {
		// Three entries over two fields: QR is a user session reached a different way, not a
		// third kind of session, so it stays out of AuthMode and only sets LoginMethod.
		mode := draft.AuthMode
		method := draft.LoginMethod
		initial := 0
		switch {
		case mode == config.AuthBot:
			initial = 2
		case method == config.LoginQR:
			initial = 1
		}
		form := tview.NewForm()
		form.AddDropDown(i18n.T(i18n.KeyOnboardingAuthMode), []string{
			i18n.T(i18n.KeyOnboardingAuthUser),
			i18n.T(i18n.KeyOnboardingLoginQR),
			i18n.T(i18n.KeyOnboardingAuthBot),
		}, initial, func(_ string, index int) {
			switch index {
			case 2:
				mode = config.AuthBot
				method = config.LoginPhone
			case 1:
				mode = config.AuthUser
				method = config.LoginQR
			default:
				mode = config.AuthUser
				method = config.LoginPhone
			}
		}).
			AddButton(i18n.T(i18n.KeyOnboardingNext), func() {
				draft.AuthMode = mode
				draft.LoginMethod = method
				status.SetText(i18n.T(i18n.KeyOnboardingStatusModeSaved))
				showAPI()
			}).
			AddButton(i18n.T(i18n.KeyOnboardingBack), showLanguage)
		setPage(i18n.T(i18n.KeyOnboardingAuthTitle), i18n.T(i18n.KeyOnboardingAuthBody), form)
	}

	showAPI = func() {
		apiID := tview.NewInputField().
			SetLabel(i18n.T(i18n.KeyOnboardingAPIID) + ": ").
			SetText(apiIDText(draft.APIID)).
			SetFieldWidth(18)
		apiHash := tview.NewInputField().
			SetLabel(i18n.T(i18n.KeyOnboardingAPIHash) + ": ").
			SetText(draft.APIHash).
			SetFieldWidth(36)
		form := tview.NewForm().
			AddFormItem(apiID).
			AddFormItem(apiHash).
			AddButton(i18n.T(i18n.KeyOnboardingNext), func() {
				id, err := strconv.Atoi(strings.TrimSpace(apiID.GetText()))
				if err != nil || id <= 0 {
					status.SetText(i18n.T(i18n.KeyOnboardingStatusAPIIDInvalid))
					return
				}
				hash := strings.TrimSpace(apiHash.GetText())
				if hash == "" {
					status.SetText(i18n.T(i18n.KeyOnboardingStatusAPIHashRequired))
					return
				}
				draft.APIID = id
				draft.APIHash = hash
				status.SetText(i18n.T(i18n.KeyOnboardingStatusAPISaved))
				showIdentity()
			}).
			AddButton(i18n.T(i18n.KeyOnboardingBack), showAuthMode)
		// Bot mode gets its own wording. Anyone arriving from the HTTP Bot API reasonably expects a
		// token to be enough, and the answer - that this client speaks MTProto, where the api_id is
		// passed both to open the connection and to log the bot in - is the whole explanation.
		bodyKey := i18n.KeyOnboardingAPIBody
		if draft.AuthMode == config.AuthBot {
			bodyKey = i18n.KeyOnboardingAPIBodyBot
		}
		setPage(i18n.T(i18n.KeyOnboardingAPITitle), i18n.T(bodyKey), form)
	}

	showIdentity = func() {
		if draft.AuthMode == config.AuthUser && draft.LoginMethod == config.LoginQR {
			// Nothing to enter: the code is scanned. This is the "separate auth path rather
			// than another form page" the onboarding design calls for.
			showSummary()
			return
		}
		labelKey := i18n.KeyOnboardingPhone
		value := draft.Phone
		secret := false
		body := i18n.T(i18n.KeyOnboardingPhoneBody)
		if draft.AuthMode == config.AuthBot {
			labelKey = i18n.KeyOnboardingBotToken
			value = draft.BotToken
			secret = true
			body = i18n.T(i18n.KeyOnboardingBotBody)
		}
		input := tview.NewInputField().
			SetLabel(i18n.T(labelKey) + ": ").
			SetText(value).
			SetFieldWidth(40)
		if secret {
			input.SetMaskCharacter('*')
		}
		form := tview.NewForm().
			AddFormItem(input).
			AddButton(i18n.T(i18n.KeyOnboardingNext), func() {
				value := strings.TrimSpace(input.GetText())
				if value == "" {
					status.SetText(i18n.T(i18n.KeyOnboardingStatusIdentityRequired))
					return
				}
				if draft.AuthMode == config.AuthBot {
					draft.BotToken = value
					draft.Phone = ""
				} else {
					draft.Phone = value
					draft.BotToken = ""
				}
				status.SetText(i18n.T(i18n.KeyOnboardingStatusIdentitySaved))
				showSummary()
			}).
			AddButton(i18n.T(i18n.KeyOnboardingBack), showAPI)
		setPage(i18n.T(i18n.KeyOnboardingIdentityTitle), body, form)
	}

	showSummary = func() {
		mode := i18n.T(i18n.KeyOnboardingAuthUser)
		identity := draft.Phone
		switch {
		case draft.AuthMode == config.AuthBot:
			mode = i18n.T(i18n.KeyOnboardingAuthBot)
			identity = i18n.T(i18n.KeyOnboardingBotTokenHidden)
		case draft.LoginMethod == config.LoginQR:
			mode = i18n.T(i18n.KeyOnboardingLoginQR)
			identity = i18n.T(i18n.KeyOnboardingLoginQR)
		}
		body := fmt.Sprintf("%s\n\n%s: %s\n%s: %d\n%s: %s\n%s",
			i18n.T(i18n.KeyOnboardingSummaryBody),
			i18n.T(i18n.KeyOnboardingAuthMode), mode,
			i18n.T(i18n.KeyOnboardingAPIID), draft.APIID,
			i18n.T(i18n.KeyOnboardingIdentity), identity,
			onboardingProxyLine(draft),
		)
		form := tview.NewForm().
			AddButton(i18n.T(i18n.KeyOnboardingConnect), func() {
				a.completeOnboarding(draft, status)
			}).
			AddButton(i18n.T(i18n.KeyOnboardingBack), showIdentity)
		setPage(i18n.T(i18n.KeyOnboardingSummaryTitle), body, form)
	}

	showWelcome()
}

func (a *App) completeOnboarding(next config.Config, status *tview.TextView) {
	if next.NeedsOnboarding() || !next.ReadyForTelegram() {
		status.SetText(i18n.T(i18n.KeyOnboardingStatusIncomplete))
		return
	}
	a.cfg = next
	a.footer.SetText(render.Footer(string(a.cfg.AuthMode), version.String(), a.cfg.Proxy))
	reply := make(chan error, 1)
	if a.control != nil {
		a.control <- ControlEvent{Kind: ControlOnboardingComplete, Config: next, Reply: reply}
	} else {
		reply <- nil
	}
	status.SetText(i18n.T(i18n.KeyOnboardingStatusConnecting))
	go func() {
		err := <-reply
		a.app.QueueUpdateDraw(func() {
			if err != nil {
				status.SetText(i18n.Tf(i18n.KeyStatusError, err.Error()))
				return
			}
			a.onboardingActive = false
			a.resetTelegramView(i18n.T(i18n.KeyUIWelcomeConnect))
			a.app.SetRoot(a.root, true)
			a.app.SetFocus(a.chats)
			a.updateFocusStyle()
			a.setStatusMsg(i18n.KeyStatusConnecting)
		})
	}()
}

func (a *App) resetTelegramView(message string) {
	a.connectedAs = ""
	a.currentChat = ""
	a.currentTitle = ""
	a.currentFolder = 0
	a.currentBroadcast = false
	a.currentGroupRead = false
	a.allChats = nil
	a.allFolders = nil
	a.chatsHighlightPeer = ""
	a.clearReplyTarget()
	a.folders.Clear()
	a.folders.AddItem(i18n.T(i18n.KeyUIFolderAll), "", 0, func() {
		a.currentFolder = 0
		a.refreshChats()
	})
	a.chats.Clear()
	a.chats.AddItem("Tsumugi", i18n.T(i18n.KeyUIWaitingConnection), 0, func() {
		a.currentChat = "welcome"
		a.currentTitle = "Tsumugi"
		a.commands <- telegram.Command{Kind: telegram.CommandFocusChat, PeerKey: ""}
		a.applyMessagesPaneTitle()
		a.messages.SetText(i18n.T(i18n.KeyUIWelcomeConnect))
	})
	a.messages.SetText(message)
	a.footer.SetText(render.Footer(string(a.cfg.AuthMode), version.String(), a.cfg.Proxy))
	a.refreshStatusBar()
}

func onboardingProxyLine(cfg config.Config) string {
	for _, entry := range cfg.Proxy.UIEntries() {
		if entry.Active {
			return i18n.Tf(i18n.KeyOnboardingProxySummary, entry.Description)
		}
	}
	return i18n.Tf(i18n.KeyOnboardingProxySummary, i18n.T(i18n.KeyProxyNoProxyDesc))
}

func apiIDText(id int) string {
	if id == 0 {
		return ""
	}
	return strconv.Itoa(id)
}
