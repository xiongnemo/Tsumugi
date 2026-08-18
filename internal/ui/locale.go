package ui

import (
	"fmt"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
	"github.com/nemo/Tsumugi/internal/version"
)

func (a *App) applyMainLocale() {
	a.folders.SetTitle(" " + i18n.T(i18n.KeyUIFolders) + " ")
	a.chats.SetTitle(" " + i18n.T(i18n.KeyUIChats) + " ")
	a.composer.SetTitle(" " + i18n.T(i18n.KeyUICompose) + " ")
	a.composer.SetPlaceholder(i18n.T(i18n.KeyUIComposePlaceholder))
	a.statusBar.SetTitle(" " + i18n.T(i18n.KeyUIStatus) + " ")
	a.footer.SetText(render.Footer(string(a.cfg.AuthMode), version.String(), a.cfg.Proxy))
	a.applyMessagesPaneTitle()
	a.applyMessagesPlaceholder()
	if len(a.allChats) > 0 {
		a.refreshFolders()
		a.refreshChats()
	} else if a.chats.GetItemCount() > 0 {
		a.refreshWelcomeChatRow()
	}
	a.refreshStatusBar()
}

func (a *App) applyMessagesPlaceholder() {
	if a.currentChat != "" && a.currentChat != "welcome" {
		return
	}
	if a.connectedAs != "" {
		a.messages.SetText(a.connectedMessage())
		return
	}
	if a.currentChat == "welcome" {
		a.messages.SetText(i18n.T(i18n.KeyUIWelcomeConnect))
		return
	}
	a.messages.SetText(i18n.T(i18n.KeyUIWelcomeHint))
}

func (a *App) connectedMessage() string {
	return fmt.Sprintf("%s\n\n%s",
		fmt.Sprintf(i18n.T(i18n.KeyUIConnectedAs), a.connectedAs),
		i18n.T(i18n.KeyUISelectChat),
	)
}

func (a *App) refreshWelcomeChatRow() {
	if a.chats.GetItemCount() == 0 {
		return
	}
	main, _ := a.chats.GetItemText(0)
	if main != "Tsumugi" {
		return
	}
	a.chats.SetItemText(0, main, i18n.T(i18n.KeyUIWaitingConnection))
}

func localizedChatDisplay(chat telegram.Chat) telegram.Chat {
	out := chat
	out.Subtitle = i18n.ChatKind(chat.Subtitle)
	if chat.PreviewKey != "" {
		// Generated preview (media placeholder, empty marker, service message): rebuild
		// it from the stored key so the locale switch is exact.
		out.LastPreview = i18n.PreviewText(chat.PreviewKey, chat.PreviewArg)
		return out
	}
	// Rows saved before preview keys existed still fall back to reverse lookup; rows with
	// user-authored text are returned untouched by LocalizeKnown.
	out.LastPreview = i18n.LocalizeKnown(chat.LastPreview)
	return out
}
