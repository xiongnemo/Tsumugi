package render

import (
	"strconv"
	"strings"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// pollBarWidth is how many cells the share bar occupies. Narrow on purpose: the bar is a hint at the
// shape of the answers, and every cell it takes is one the option text does not get.
const pollBarWidth = 10

// PollLines renders a poll as display lines: the question, one line per option, then a footer.
//
// Called from MessageRowLines, which is the single place a message becomes lines, so nothing else in
// the UI needs to know polls exist. Before this a poll was the bare label "[Poll]" - no question, no
// options, no counts, which made it unreadable rather than merely unanswerable.
func PollLines(poll *telegram.PollSummary, width int) []string {
	if poll == nil {
		return nil
	}
	// Nothing here truncates an already-tagged string: Truncate measures tags as visible width and
	// would happily cut one in half, and a broken [color tag renders as literal text.
	lines := []string{pollHeader(poll, width)}
	for _, option := range poll.Options {
		lines = append(lines, pollOptionLine(poll, option, width))
	}
	return append(lines, pollFooter(poll, width))
}

func pollHeader(poll *telegram.PollSummary, width int) string {
	kind := i18n.T(i18n.KeyPollTitle)
	if poll.Quiz {
		kind = i18n.T(i18n.KeyPollQuiz)
	}
	question := strings.TrimSpace(poll.Question)
	if question == "" {
		return "[yellow]" + Truncate(kind, width) + "[-]"
	}
	room := width - DisplayWidth(kind) - 1
	return "[yellow]" + kind + "[-] " + Truncate(question, room)
}

// pollOptionLine is one answer: a marker, the share bar, the percentage and the text.
//
// The marker carries the meaning that colour alone cannot: a bare highlight would be invisible on a
// 16-colour console, and the raw TTY is a target.
func pollOptionLine(poll *telegram.PollSummary, option telegram.PollOption, width int) string {
	glyphs := Glyphs()
	marker := "  "
	switch {
	case option.Chosen && poll.Quiz && !option.Correct:
		marker = "[red]" + glyphs.PollWrong + "[-] "
	case option.Chosen:
		marker = "[green]" + glyphs.Marked + "[-] "
	case poll.Quiz && option.Correct && poll.Voted:
		// Only after answering: revealing the right answer earlier would give the quiz away.
		marker = "[green]" + glyphs.PollRight + "[-] "
	}
	percent := pollPercent(option.Voters, poll.TotalVoters)
	bar := pollBar(percent)
	share := strconv.Itoa(percent) + "%"
	// Text last so that truncation eats the answer rather than the numbers, which are what the line
	// is for once a poll has been answered.
	text := strings.TrimSpace(option.Text)
	// Everything but the text is fixed width, so this is the only part that can overflow. 6 covers
	// the leading indent, the two-cell marker and the two separating spaces.
	room := width - DisplayWidth(bar) - DisplayWidth(share) - 6
	if room < 0 {
		room = 0
	}
	return "  " + marker + bar + " " + share + " " + Truncate(text, room)
}

func pollFooter(poll *telegram.PollSummary, width int) string {
	parts := make([]string, 0, 3)
	if poll.TotalVoters > 0 {
		parts = append(parts, i18n.Tf(i18n.KeyPollTotalVoters, poll.TotalVoters))
	} else {
		parts = append(parts, i18n.T(i18n.KeyPollNoVotes))
	}
	if poll.MultipleChoice {
		parts = append(parts, i18n.T(i18n.KeyPollMultiple))
	}
	if poll.PublicVoters {
		parts = append(parts, i18n.T(i18n.KeyPollPublic))
	} else {
		parts = append(parts, i18n.T(i18n.KeyPollAnonymous))
	}
	if poll.Closed {
		parts = append(parts, i18n.T(i18n.KeyPollClosed))
	}
	return "  [gray]" + Truncate(strings.Join(parts, " · "), width-2) + "[-]"
}

// pollPercent is the share of the vote, rounded to the nearest whole percent.
func pollPercent(voters, total int) int {
	if total <= 0 || voters <= 0 {
		return 0
	}
	// Rounding rather than truncating: three equal answers should read 33/33/33, not 33/33/33 with
	// a 34 hidden by truncation somewhere else.
	percent := (voters*200 + total) / (total * 2)
	if percent > 100 {
		return 100
	}
	return percent
}

// pollBar draws the share as a fixed-width bar.
func pollBar(percent int) string {
	filled := (percent*pollBarWidth + 50) / 100
	if filled > pollBarWidth {
		filled = pollBarWidth
	}
	if filled < 0 {
		filled = 0
	}
	glyphs := Glyphs()
	return strings.Repeat(glyphs.PollFilled, filled) + strings.Repeat(glyphs.PollEmpty, pollBarWidth-filled)
}
