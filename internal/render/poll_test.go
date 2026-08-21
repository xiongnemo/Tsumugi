package render

import (
	"strings"
	"testing"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func testPoll() *telegram.PollSummary {
	return &telegram.PollSummary{
		Question:    "Lunch?",
		TotalVoters: 4,
		Options: []telegram.PollOption{
			{Text: "Ramen", Option: []byte{1}, Voters: 3, Chosen: true},
			{Text: "Curry", Option: []byte{2}, Voters: 1},
			{Text: "Nothing", Option: []byte{3}},
		},
	}
}

// A poll used to render as the bare label "[Poll]": no question, no options, no counts. Unreadable,
// not merely unanswerable.
func TestPollLinesShowQuestionOptionsAndCounts(t *testing.T) {
	lines := PollLines(testPoll(), 60)
	if len(lines) != 5 {
		t.Fatalf("%d lines, want question + 3 options + footer:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "Lunch?") {
		t.Errorf("first line = %q, want the question", lines[0])
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Ramen", "Curry", "Nothing", "75%", "25%", "0%"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rendered poll is missing %q:\n%s", want, joined)
		}
	}
	// The answer this account gave has to be visible without relying on colour: a 16-colour console
	// is a target, and the raw TTY has no styling to spare.
	if !strings.Contains(lines[1], Glyphs().Marked) {
		t.Errorf("chosen option = %q, want a marker glyph", lines[1])
	}
}

func TestPollPercentRounds(t *testing.T) {
	cases := []struct {
		voters, total, want int
	}{
		{0, 0, 0},
		{0, 10, 0},
		{1, 3, 33},
		{2, 3, 67},
		{1, 2, 50},
		{10, 10, 100},
		// Counts can briefly exceed the total while an update is in flight.
		{11, 10, 100},
	}
	for _, tc := range cases {
		if got := pollPercent(tc.voters, tc.total); got != tc.want {
			t.Errorf("pollPercent(%d, %d) = %d, want %d", tc.voters, tc.total, got, tc.want)
		}
	}
}

// The bar is fixed width so the option text starts in the same column on every row.
func TestPollBarIsFixedWidth(t *testing.T) {
	for _, percent := range []int{0, 1, 33, 50, 99, 100} {
		if got := DisplayWidth(pollBar(percent)); got != pollBarWidth {
			t.Errorf("pollBar(%d) is %d cells, want %d", percent, got, pollBarWidth)
		}
	}
}

// A quiz must not give its answer away before it is answered.
func TestQuizHidesTheAnswerUntilVoted(t *testing.T) {
	poll := testPoll()
	poll.Quiz = true
	poll.Voted = false
	poll.Options = []telegram.PollOption{
		{Text: "Right", Option: []byte{1}, Correct: true},
		{Text: "Wrong", Option: []byte{2}},
	}

	before := strings.Join(PollLines(poll, 60), "\n")
	if strings.Contains(before, Glyphs().PollRight) {
		t.Fatalf("the correct answer is marked before voting:\n%s", before)
	}

	poll.Voted = true
	after := strings.Join(PollLines(poll, 60), "\n")
	if !strings.Contains(after, Glyphs().PollRight) {
		t.Fatalf("the correct answer is not marked after voting:\n%s", after)
	}
}

// Every line has to fit: the viewport draws them as-is, and an over-wide line would be clipped
// mid-escape-sequence.
func TestPollLinesRespectWidth(t *testing.T) {
	poll := testPoll()
	poll.Question = strings.Repeat("very long question ", 10)
	poll.Options[0].Text = strings.Repeat("verbose answer ", 10)
	for _, width := range []int{30, 40, 60, 100} {
		for _, line := range PollLines(poll, width) {
			// Tags count toward the measured width here, the same way Truncate measures them
			// everywhere else in this package, so a line inside the budget is inside it on screen too.
			if got := DisplayWidth(line); got > width {
				t.Errorf("width %d: line is %d cells: %q", width, got, line)
			}
			// A truncated colour tag renders as literal text, which is the failure this shape avoids.
			if opens, closes := strings.Count(line, "["), strings.Count(line, "]"); opens != closes {
				t.Errorf("width %d: unbalanced tags in %q", width, line)
			}
		}
	}
}

func TestPollLinesNilIsEmpty(t *testing.T) {
	if lines := PollLines(nil, 40); len(lines) != 0 {
		t.Fatalf("lines = %v, want none for a message with no poll", lines)
	}
}
