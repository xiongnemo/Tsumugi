package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

func (a *App) toggleOutgoingLayout() {
	if a.messages.LayoutRelayoutBusy() {
		return
	}
	if a.settings.OutgoingLayout == string(render.LayoutIM) {
		a.settings.OutgoingLayout = string(render.LayoutTranscript)
	} else {
		a.settings.OutgoingLayout = string(render.LayoutIM)
	}
	_ = a.settings.Save(context.Background(), a.db)
	mode := render.ParseLayoutMode(a.settings.OutgoingLayout)
	msgs := append([]telegram.Message(nil), a.messages.Messages()...)
	width := a.messages.LayoutWidthForBuild()
	broadcast := a.currentBroadcast
	groupRead := a.currentGroupRead
	inlineAnim := a.settings.InlineAnim

	a.messages.SetLayoutRelayoutBusy(true)
	a.setStatusMsg(i18n.KeyStatusLayoutSwitching)

	go func() {
		blocks := buildAllMessageBlocks(msgs, width, mode, broadcast, groupRead, inlineAnim, 0, true)
		a.app.QueueUpdateDraw(func() {
			a.messages.ApplyPrebuiltLayout(blocks, width, mode)
			a.setStatusMsg(i18n.KeyStatusLayoutMode, a.settings.OutgoingLayout)
		})
	}()
}

func (a *App) onMessageSelectionChanged() {
	if !a.currentBroadcast {
		return
	}
	msg, ok := a.selectedMessageRow()
	if !ok {
		return
	}
	id, err := strconv.Atoi(msg.ID)
	if err != nil || id <= 0 {
		return
	}
	a.commands <- telegram.Command{Kind: telegram.CommandMarkViewed, PeerKey: a.currentChat, MessageID: id}
}

func (a *App) showReactionPanel() {
	msg, ok := a.selectedMessageRow()
	if !ok {
		a.setStatusMsg(i18n.KeyStatusNoMessageSelected)
		return
	}
	a.showReactionPanelFor(msg)
}

func (a *App) showReactionPanelFor(msg telegram.Message) {
	quick := telegram.DefaultQuickReactions()
	summary := reactionSummaryText(msg)
	recent := reactionRecentText(msg)

	detail := tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true).
		SetText(summary + "\n\n" + recent)
	detail.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyReactionTitle) + " ")

	form := tview.NewForm()
	for i, reaction := range quick {
		if i >= 8 {
			break
		}
		idx := i
		label := fmt.Sprintf("%d %s", idx+1, reactionLabel(reaction))
		react := reaction
		form.AddButton(label, func() {
			a.restoreMessageFocus()
			a.sendReactionFor(msg, react)
		})
	}
	form.AddButton(i18n.T(i18n.KeyActionCancel), func() {
		a.restoreMessageFocus()
	})
	hint := tview.NewTextView().
		SetDynamicColors(true).
		SetText(i18n.T(i18n.KeyReactionQuickHint))
	hint.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyReactionQuick) + " ")

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(detail, 0, 1, false).
		AddItem(hint, 3, 0, false).
		AddItem(form, 0, 1, true)
	layout.SetBorder(true).SetTitle(" " + i18n.T(i18n.KeyReactionTitle) + " ")
	layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			a.restoreMessageFocus()
			return nil
		}
		return event
	})
	a.app.SetRoot(layout, true)
	a.app.SetFocus(form)
}

func (a *App) sendReactionFor(msg telegram.Message, reaction telegram.ReactionSummary) {
	id, err := strconv.Atoi(msg.ID)
	if err != nil || id <= 0 {
		a.setStatusMsg(i18n.KeyStatusCannotDeleteLocal)
		return
	}
	a.commands <- telegram.Command{
		Kind:      telegram.CommandSendReaction,
		PeerKey:   a.currentChat,
		MessageID: id,
		Reaction:  reaction,
	}
	a.setStatusMsg(i18n.KeyStatusReactionSent)
}

func reactionSummaryText(msg telegram.Message) string {
	if len(msg.Reactions) == 0 {
		return i18n.T(i18n.KeyReactionNone)
	}
	var parts []string
	for _, r := range msg.Reactions {
		parts = append(parts, fmt.Sprintf("%s ×%d", reactionLabel(r), r.Count))
	}
	return strings.Join(parts, "  ")
}

func reactionRecentText(msg telegram.Message) string {
	if len(msg.RecentReact) == 0 {
		return ""
	}
	var parts []string
	for _, r := range msg.RecentReact {
		parts = append(parts, fmt.Sprintf("%s %s", r.PeerName, strings.TrimSpace(r.Emoji)))
	}
	return i18n.Tf(i18n.KeyReactionRecent, strings.Join(parts, " "))
}

func reactionLabel(reaction telegram.ReactionSummary) string {
	if alt := strings.TrimSpace(reaction.CustomAlt); alt != "" {
		return alt
	}
	if emoji := strings.TrimSpace(reaction.Emoji); emoji != "" {
		return emoji
	}
	return "?"
}
