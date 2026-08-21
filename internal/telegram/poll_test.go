package telegram

import (
	"strings"
	"testing"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/storage"
)

func testPollMedia(chosen int) *tg.MessageMediaPoll {
	answers := []tg.PollAnswerClass{
		&tg.PollAnswer{Text: tg.TextWithEntities{Text: "Ramen"}, Option: []byte{1}},
		&tg.PollAnswer{Text: tg.TextWithEntities{Text: "Curry"}, Option: []byte{2}},
	}
	results := tg.PollResults{}
	results.SetTotalVoters(3)
	voters := []tg.PollAnswerVoters{
		{Option: []byte{1}},
		{Option: []byte{2}},
	}
	voters[0].SetVoters(2)
	voters[1].SetVoters(1)
	if chosen >= 0 && chosen < len(voters) {
		voters[chosen].Chosen = true
	}
	results.SetResults(voters)
	return &tg.MessageMediaPoll{
		Poll: tg.Poll{
			ID:       99,
			Question: tg.TextWithEntities{Text: "Lunch?"},
			Answers:  answers,
		},
		Results: results,
	}
}

func storageMessageForTest() storage.Message {
	return storage.Message{AccountID: "acct", PeerKey: "chat:1", ID: 5, State: "synced"}
}

// A poll was classified as the bare label "[Poll]" and nothing else, so the question and the answers
// never reached the UI at all.
func TestPollSummaryReadsQuestionOptionsAndCounts(t *testing.T) {
	summary := pollSummary(testPollMedia(0))
	if summary == nil {
		t.Fatal("no summary")
	}
	if summary.Question != "Lunch?" {
		t.Errorf("Question = %q", summary.Question)
	}
	if summary.TotalVoters != 3 {
		t.Errorf("TotalVoters = %d, want 3", summary.TotalVoters)
	}
	if len(summary.Options) != 2 {
		t.Fatalf("%d options, want 2", len(summary.Options))
	}
	if summary.Options[0].Voters != 2 || summary.Options[1].Voters != 1 {
		t.Errorf("voters = %d/%d, want 2/1", summary.Options[0].Voters, summary.Options[1].Voters)
	}
	if !summary.Options[0].Chosen || !summary.Voted {
		t.Error("the answer this account gave was not recorded")
	}
	// The opaque token, not an index: a poll can shuffle its answers, and voting by index would
	// then record the wrong one.
	if len(summary.Options[0].Option) != 1 || summary.Options[0].Option[0] != 1 {
		t.Errorf("option token = %v, want Telegram's bytes", summary.Options[0].Option)
	}
}

// A poll nobody has answered has no results list at all.
func TestPollSummaryHandlesNoResults(t *testing.T) {
	media := &tg.MessageMediaPoll{
		Poll: tg.Poll{
			ID:       1,
			Question: tg.TextWithEntities{Text: "Anyone?"},
			Answers:  []tg.PollAnswerClass{&tg.PollAnswer{Text: tg.TextWithEntities{Text: "Yes"}, Option: []byte{7}}},
		},
	}
	summary := pollSummary(media)
	if summary == nil || len(summary.Options) != 1 {
		t.Fatalf("summary = %+v, want one option", summary)
	}
	if summary.TotalVoters != 0 || summary.Voted || summary.Options[0].Voters != 0 {
		t.Errorf("summary = %+v, want an unanswered poll", summary)
	}
}

// Classification has to attach the summary, or none of the rendering ever sees it.
func TestClassifyMediaAttachesThePoll(t *testing.T) {
	attachment := classifyMessageMedia(testPollMedia(-1))
	if attachment.Kind != "poll" {
		t.Fatalf("Kind = %q, want poll", attachment.Kind)
	}
	if attachment.Poll == nil {
		t.Fatal("the poll summary was dropped during classification")
	}
	if attachment.Poll.Question != "Lunch?" {
		t.Errorf("Question = %q", attachment.Poll.Question)
	}
}

// The summary travels in the media JSON column, so a poll read offline still has its question and
// options rather than degrading to the old bare label.
func TestPollSurvivesTheMediaJSONRoundTrip(t *testing.T) {
	original := classifyMessageMedia(testPollMedia(1))
	stored := applyMediaToStoredMessage(storageMessageForTest(), original)
	if stored == nil {
		t.Fatal("media could not be encoded")
	}
	decoded, ok := decodeMediaAttachment(stored.MediaJSON)
	if !ok {
		t.Fatal("media could not be decoded")
	}
	if decoded.Poll == nil {
		t.Fatal("the poll did not survive the round trip")
	}
	if decoded.Poll.Question != "Lunch?" || len(decoded.Poll.Options) != 2 {
		t.Fatalf("decoded poll = %+v", decoded.Poll)
	}
	if !decoded.Poll.Options[1].Chosen {
		t.Error("the chosen answer did not survive")
	}
	if len(decoded.Poll.Options[1].Option) == 0 {
		t.Error("the option token did not survive, so the poll can no longer be voted on")
	}
}

// Media with no poll must not gain an empty one: the field is omitted, and every other kind's JSON
// stays exactly as it was.
func TestNonPollMediaHasNoPollField(t *testing.T) {
	stored := applyMediaToStoredMessage(storageMessageForTest(), MediaAttachment{Kind: "photo"})
	if stored == nil {
		t.Fatal("media could not be encoded")
	}
	if want := `"Poll"`; strings.Contains(stored.MediaJSON, want) {
		t.Fatalf("photo media JSON contains %s: %s", want, stored.MediaJSON)
	}
}
