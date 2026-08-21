package telegram

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

// PollOption is one answer of a poll.
//
// Option is Telegram's opaque token for the answer, not an index: voting sends these bytes back, and
// they are the only thing the server accepts.
type PollOption struct {
	Text    string
	Option  []byte
	Voters  int
	Chosen  bool
	Correct bool
}

// PollSummary is a poll as Tsumugi shows it.
//
// Carried inside MediaAttachment and therefore through the MediaJSON column, so a poll read offline
// still shows its question and options. The counts are a snapshot and can be stale - the same trade
// view counts and reactions already make.
type PollSummary struct {
	ID             int64
	Question       string
	Options        []PollOption
	TotalVoters    int
	Closed         bool
	Quiz           bool
	MultipleChoice bool
	PublicVoters   bool
	// ResultsMin marks results that are the same for every user and therefore carry no "which
	// option did I choose". Only messages.getPollResults can fill that in; without the follow-up a
	// user's own vote never shows as chosen. See refreshPollResults.
	ResultsMin bool
	// Voted is true when this account has already answered.
	Voted bool
}

// pollSummary reads a poll and its results out of a message's media.
func pollSummary(media *tg.MessageMediaPoll) *PollSummary {
	if media == nil {
		return nil
	}
	poll := media.Poll
	out := &PollSummary{
		ID:             poll.ID,
		Question:       poll.Question.Text,
		Closed:         poll.Closed,
		Quiz:           poll.Quiz,
		MultipleChoice: poll.MultipleChoice,
		PublicVoters:   poll.PublicVoters,
		ResultsMin:     media.Results.Min,
	}
	if total, ok := media.Results.GetTotalVoters(); ok {
		out.TotalVoters = total
	}
	// Voter counts live in a parallel list keyed by the same opaque option token, and it can be
	// absent entirely on a poll nobody has answered.
	voters := make(map[string]tg.PollAnswerVoters)
	if results, ok := media.Results.GetResults(); ok {
		for _, result := range results {
			voters[string(result.Option)] = result
		}
	}
	for _, item := range poll.Answers {
		answer, ok := item.(*tg.PollAnswer)
		if !ok {
			continue
		}
		option := PollOption{Text: answer.Text.Text, Option: answer.Option}
		if result, ok := voters[string(answer.Option)]; ok {
			if count, ok := result.GetVoters(); ok {
				option.Voters = count
			}
			option.Chosen = result.Chosen
			option.Correct = result.Correct
			if result.Chosen {
				out.Voted = true
			}
		}
		out.Options = append(out.Options, option)
	}
	return out
}

// sendVote answers a poll.
//
// Options are the opaque tokens from PollSummary, never indices: Telegram matches the bytes, and an
// index would silently vote for the wrong answer whenever the poll shuffles its options.
func (c *GotdClient) sendVote(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, command Command) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return
	}
	if len(command.PollOptions) == 0 {
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusPollNoChoice)})
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, command.PeerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", command.PeerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}

	send := c.voteSend
	if send == nil {
		send = api.MessagesSendVote
	}
	updates, err := retryFloodWait(ctx, defaultMaxFloodWaits, "voting", func(ctx context.Context) (tg.UpdatesClass, error) {
		return send(ctx, &tg.MessagesSendVoteRequest{
			Peer:    input,
			MsgID:   command.MessageID,
			Options: command.PollOptions,
		})
	})
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, PeerKey: command.PeerKey, Error: rpcError("vote", err)})
		return
	}
	// The response carries updateMessagePoll with the new counts. Reusing the edit collector would
	// not help: a poll vote does not produce an edited message, it produces a new poll object.
	updated := c.messagesFromPollUpdates(ctx, accountID, command.PeerKey, command.MessageID, updates)
	if len(updated) == 1 {
		// Min results are the same for every user and carry no "which option did I choose", which in
		// a channel means the vote just cast would not show as chosen. One follow-up fixes that.
		if media, ok := decodeMediaAttachment(updated[0].MediaJSON); ok && media.Poll != nil && media.Poll.ResultsMin {
			if full := c.fetchPollResults(ctx, api, input, command.MessageID); full != nil {
				if merged := c.mergeStoredPollResults(ctx, accountID, command.PeerKey, command.MessageID, *full); merged != nil {
					updated = []storage.Message{*merged}
				}
			}
		}
	}
	if len(updated) > 0 {
		_ = c.store.SaveMessages(ctx, updated)
		sendEvent(ctx, events, Event{
			Kind:      EventMessages,
			PeerKey:   command.PeerKey,
			Messages:  c.telegramMessages(ctx, accountID, updated),
			Patch:     true,
			StatusMsg: i18n.M(i18n.KeyStatusPollVoted),
		})
		return
	}
	sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusPollVoted)})
}

// fetchPollResults asks for the full results of a poll whose update carried only the shared ones.
//
// Best effort: a failure here leaves the counts we already have, which are correct apart from not
// knowing our own answer.
func (c *GotdClient) fetchPollResults(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, messageID int) *tg.PollResults {
	if api == nil {
		return nil
	}
	updates, err := retryFloodWait(ctx, 1, "poll results", func(ctx context.Context) (tg.UpdatesClass, error) {
		return api.MessagesGetPollResults(ctx, &tg.MessagesGetPollResultsRequest{Peer: peer, MsgID: messageID})
	})
	if err != nil {
		return nil
	}
	var list []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		list = u.Updates
	case *tg.UpdatesCombined:
		list = u.Updates
	case *tg.UpdateShort:
		list = []tg.UpdateClass{u.Update}
	}
	for _, update := range list {
		if poll, ok := update.(*tg.UpdateMessagePoll); ok {
			results := poll.Results
			return &results
		}
	}
	return nil
}

// messagesFromPollUpdates rewrites the stored message with the poll from an update.
func (c *GotdClient) messagesFromPollUpdates(ctx context.Context, accountID, peerKey string, messageID int, updates tg.UpdatesClass) []storage.Message {
	var list []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		list = u.Updates
	case *tg.UpdatesCombined:
		list = u.Updates
	case *tg.UpdateShort:
		list = []tg.UpdateClass{u.Update}
	}
	for _, update := range list {
		poll, ok := update.(*tg.UpdateMessagePoll)
		if !ok {
			continue
		}
		media := &tg.MessageMediaPoll{Results: poll.Results}
		if full, ok := poll.GetPoll(); ok {
			media.Poll = full
		} else {
			// A results-only update: the question and answers are whatever we already stored, so
			// merge the new counts into the existing summary instead of dropping them.
			if merged := c.mergeStoredPollResults(ctx, accountID, peerKey, messageID, poll.Results); merged != nil {
				return []storage.Message{*merged}
			}
			continue
		}
		if updated := c.applyPollToStoredMessage(ctx, accountID, peerKey, messageID, pollSummary(media)); updated != nil {
			return []storage.Message{*updated}
		}
	}
	return nil
}

// mergeStoredPollResults updates the counts of a stored poll without touching its question.
func (c *GotdClient) mergeStoredPollResults(ctx context.Context, accountID, peerKey string, messageID int, results tg.PollResults) *storage.Message {
	stored, ok := c.storedMessage(ctx, accountID, peerKey, messageID)
	if !ok {
		return nil
	}
	media, ok := decodeMediaAttachment(stored.MediaJSON)
	if !ok || media.Poll == nil {
		return nil
	}
	summary := *media.Poll
	if total, hasTotal := results.GetTotalVoters(); hasTotal {
		summary.TotalVoters = total
	}
	summary.ResultsMin = results.Min
	if list, hasResults := results.GetResults(); hasResults {
		byOption := make(map[string]tg.PollAnswerVoters, len(list))
		for _, item := range list {
			byOption[string(item.Option)] = item
		}
		summary.Voted = false
		for i := range summary.Options {
			result, found := byOption[string(summary.Options[i].Option)]
			if !found {
				continue
			}
			if count, hasCount := result.GetVoters(); hasCount {
				summary.Options[i].Voters = count
			}
			summary.Options[i].Chosen = result.Chosen
			summary.Options[i].Correct = result.Correct
			if result.Chosen {
				summary.Voted = true
			}
		}
	}
	media.Poll = &summary
	return applyMediaToStoredMessage(stored, media)
}

// applyPollToStoredMessage replaces a stored message's poll wholesale.
func (c *GotdClient) applyPollToStoredMessage(ctx context.Context, accountID, peerKey string, messageID int, summary *PollSummary) *storage.Message {
	if summary == nil {
		return nil
	}
	stored, ok := c.storedMessage(ctx, accountID, peerKey, messageID)
	if !ok {
		return nil
	}
	media, ok := decodeMediaAttachment(stored.MediaJSON)
	if !ok {
		media = MediaAttachment{Kind: "poll", LabelKey: i18n.KeyMediaPoll, Label: i18n.T(i18n.KeyMediaPoll)}
	}
	media.Poll = summary
	return applyMediaToStoredMessage(stored, media)
}

func (c *GotdClient) storedMessage(ctx context.Context, accountID, peerKey string, messageID int) (storage.Message, bool) {
	if c.store == nil {
		return storage.Message{}, false
	}
	msg, ok, err := c.store.MessageByID(ctx, accountID, peerKey, messageID)
	if err != nil || !ok {
		return storage.Message{}, false
	}
	return msg, true
}

// decodeMediaAttachment reads a stored message's media back out of its JSON column.
func decodeMediaAttachment(raw string) (MediaAttachment, bool) {
	if raw == "" {
		return MediaAttachment{}, false
	}
	var media MediaAttachment
	if err := json.Unmarshal([]byte(raw), &media); err != nil {
		return MediaAttachment{}, false
	}
	return media, true
}

// applyMediaToStoredMessage writes media back into a stored row, or reports nil when it cannot be
// encoded - in which case the old row is left alone rather than being blanked.
func applyMediaToStoredMessage(stored storage.Message, media MediaAttachment) *storage.Message {
	raw, err := json.Marshal(media)
	if err != nil {
		return nil
	}
	stored.MediaJSON = string(raw)
	stored.MediaKind = media.Kind
	return &stored
}
