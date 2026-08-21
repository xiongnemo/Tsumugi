package ui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// openPollVote lists a poll's answers so one can be chosen.
//
// An overlay rather than keys on the message row: a poll can have ten answers, and the message pane
// already spends its keys on navigation. Multiple-choice polls mark several rows before committing,
// which is only possible with somewhere to hold the marks.
func (a *App) openPollVote(msg telegram.Message) {
	poll := msg.Media.Poll
	if poll == nil {
		a.setStatusMsg(i18n.KeyStatusPollNone)
		return
	}
	if poll.Closed {
		a.setStatusMsg(i18n.KeyStatusPollClosed)
		return
	}
	messageID, err := strconv.Atoi(msg.ID)
	if err != nil || messageID <= 0 {
		a.setStatusMsg(i18n.KeyStatusPollNone)
		return
	}

	list := tview.NewList().ShowSecondaryText(false)
	hint := tview.NewTextView().SetDynamicColors(true).SetText("[gray]" + i18n.T(i18n.KeyPollHint))

	a.pollList = list
	a.pollPeer = msg.ChatID
	a.pollMessageID = messageID
	a.pollSummary = poll
	a.pollMarked = make(map[int]bool, len(poll.Options))

	for _, option := range poll.Options {
		list.AddItem(pollVoteRow(option, false), "", 0, nil)
	}
	list.SetSelectedFunc(func(index int, _, _ string, _ rune) {
		if !poll.MultipleChoice {
			a.commitPollVote([]int{index})
			return
		}
		// Multiple choice: Enter toggles, and the send key commits. Committing on the first Enter
		// would make a second choice impossible.
		a.pollMarked[index] = !a.pollMarked[index]
		list.SetItemText(index, pollVoteRow(poll.Options[index], a.pollMarked[index]), "")
	})

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(list, 0, 1, true).
		AddItem(hint, 1, 0, false)
	title := i18n.T(i18n.KeyPollTitle)
	if poll.Quiz {
		title = i18n.T(i18n.KeyPollQuiz)
	}
	if question := strings.TrimSpace(poll.Question); question != "" {
		title += " · " + render.Truncate(question, 48)
	}
	layout.SetBorder(true).SetTitle(" " + title + " ")

	a.app.SetRoot(layout, true)
	a.app.SetFocus(list)
}

// pollVoteRow is one answer row, with its current mark and count.
func pollVoteRow(option telegram.PollOption, marked bool) string {
	marker := "  "
	if marked {
		marker = render.Glyphs().Marked + " "
	} else if option.Chosen {
		marker = render.Glyphs().PollRight + " "
	}
	text := strings.TrimSpace(option.Text)
	if option.Voters > 0 {
		text += "  [gray](" + strconv.Itoa(option.Voters) + ")[-]"
	}
	return marker + text
}

func (a *App) closePollVote() {
	a.pollList = nil
	a.pollSummary = nil
	a.pollMarked = nil
	a.pollPeer = ""
	a.pollMessageID = 0
	a.restoreMessageFocus()
}

// commitPollVote sends the chosen answers as their opaque tokens.
func (a *App) commitPollVote(indices []int) {
	if a.pollSummary == nil {
		a.closePollVote()
		return
	}
	options := make([][]byte, 0, len(indices))
	for _, index := range indices {
		if index < 0 || index >= len(a.pollSummary.Options) {
			continue
		}
		// The token, never the index: Telegram matches the bytes, and a poll that shuffles its
		// answers would otherwise record a vote for the wrong one.
		options = append(options, a.pollSummary.Options[index].Option)
	}
	if len(options) == 0 {
		a.setStatusMsg(i18n.KeyStatusPollNoChoice)
		return
	}
	a.commands <- telegram.Command{
		Kind:        telegram.CommandSendVote,
		PeerKey:     a.pollPeer,
		MessageID:   a.pollMessageID,
		PollOptions: options,
	}
	a.closePollVote()
}

// commitMarkedPollVote sends every marked answer, for a multiple-choice poll.
func (a *App) commitMarkedPollVote() {
	indices := make([]int, 0, len(a.pollMarked))
	for index, marked := range a.pollMarked {
		if marked {
			indices = append(indices, index)
		}
	}
	if len(indices) == 0 {
		a.setStatusMsg(i18n.KeyStatusPollNoChoice)
		return
	}
	a.commitPollVote(indices)
}

// capturePollVote handles the keys of the vote overlay.
func (a *App) capturePollVote(event *tcell.EventKey) *tcell.EventKey {
	if a.pollList == nil {
		return event
	}
	if composerSendKey(event) {
		// The same key that sends a message commits a multi-answer vote, so there is one "confirm"
		// key in the whole application rather than a special one here.
		a.commitMarkedPollVote()
		return nil
	}
	return event
}

// pollVotable reports whether a message is a poll this account can still answer.
func pollVotable(msg telegram.Message) bool {
	return msg.Media.Poll != nil && !msg.Media.Poll.Closed
}
