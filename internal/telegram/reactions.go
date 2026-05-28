package telegram

import (
	"encoding/json"
	"strings"

	"github.com/gotd/td/tg"
)

type ReactionSummary struct {
	Emoji      string `json:"emoji,omitempty"`
	Count      int    `json:"count"`
	Chosen     bool   `json:"chosen,omitempty"`
	CustomID   int64  `json:"custom_id,omitempty"`
	CustomAlt  string `json:"custom_alt,omitempty"`
}

type ReactionPeer struct {
	PeerName string `json:"peer_name"`
	Emoji    string `json:"emoji"`
}

func ParseMessageReactions(reactions *tg.MessageReactions) []ReactionSummary {
	if reactions == nil {
		return nil
	}
	results := reactions.GetResults()
	out := make([]ReactionSummary, 0, len(results))
	for _, rc := range results {
		sum := ReactionSummary{Count: rc.Count}
		if _, ok := rc.GetChosenOrder(); ok {
			sum.Chosen = true
		}
		switch emoji := rc.Reaction.(type) {
		case *tg.ReactionEmoji:
			sum.Emoji = emoji.Emoticon
		case *tg.ReactionCustomEmoji:
			sum.CustomID = emoji.DocumentID
			sum.CustomAlt = customEmojiAlt(emoji.DocumentID)
		default:
			continue
		}
		out = append(out, sum)
	}
	return out
}

func ParseRecentReactions(reactions *tg.MessageReactions, entities entitiesByID) []ReactionPeer {
	if reactions == nil {
		return nil
	}
	recent, ok := reactions.GetRecentReactions()
	if !ok {
		return nil
	}
	out := make([]ReactionPeer, 0, len(recent))
	for _, item := range recent {
		name := peerReactionName(item.PeerID, entities)
		emoji := reactionEmojiString(item.Reaction)
		if emoji == "" {
			continue
		}
		out = append(out, ReactionPeer{PeerName: name, Emoji: emoji})
	}
	return out
}

func reactionEmojiString(reaction tg.ReactionClass) string {
	switch r := reaction.(type) {
	case *tg.ReactionEmoji:
		return r.Emoticon
	case *tg.ReactionCustomEmoji:
		return customEmojiAlt(r.DocumentID)
	default:
		return ""
	}
}

func customEmojiAlt(documentID int64) string {
	if documentID == 0 {
		return ""
	}
	return ":emoji:"
}

func peerReactionName(from tg.PeerClass, entities entitiesByID) string {
	if from == nil {
		return ""
	}
	switch p := from.(type) {
	case *tg.PeerUser:
		if u := entities.users[p.UserID]; u != nil {
			return userTitle(u)
		}
		return ""
	case *tg.PeerChat:
		if c := entities.chats[p.ChatID]; c != nil {
			return c.Title
		}
		return ""
	case *tg.PeerChannel:
		if c := entities.channels[p.ChannelID]; c != nil {
			return c.Title
		}
		return ""
	default:
		return ""
	}
}

func ReactionsJSON(reactions []ReactionSummary) string {
	if len(reactions) == 0 {
		return ""
	}
	raw, err := json.Marshal(reactions)
	if err != nil {
		return ""
	}
	return string(raw)
}

func ReactionsFromJSON(raw string) []ReactionSummary {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []ReactionSummary
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func ReactionToTG(reaction ReactionSummary) tg.ReactionClass {
	if reaction.Emoji != "" {
		return &tg.ReactionEmoji{Emoticon: reaction.Emoji}
	}
	if reaction.CustomID != 0 {
		return &tg.ReactionCustomEmoji{DocumentID: reaction.CustomID}
	}
	return nil
}
