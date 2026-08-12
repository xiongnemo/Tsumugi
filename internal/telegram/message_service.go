package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

// ServiceAction describes a Telegram service ("system") message as an i18n key plus
// an optional pre-formatted argument. Display text is only produced at render time so
// a locale switch re-renders correctly, same rule as MediaAttachment.LabelKey.
//
// Invariant: Arg is non-empty exactly when Key's template takes a %s. classifyMessageAction
// enforces this by falling back to an argument-free key whenever the value is missing, so
// Msg never produces a stray %s or a %!(EXTRA) suffix.
type ServiceAction struct {
	Key string
	Arg string
}

func (a ServiceAction) Msg() i18n.Msg {
	if a.Key == "" {
		return i18n.Msg{}
	}
	if a.Arg == "" {
		return i18n.M(a.Key)
	}
	return i18n.M(a.Key, a.Arg)
}

// withArg pairs a %s key with its value, degrading to the generic key when the value is
// missing rather than rendering a literal placeholder.
func withArg(key, arg string) ServiceAction {
	if strings.TrimSpace(arg) == "" {
		return ServiceAction{Key: i18n.KeyServiceUnsupported}
	}
	return ServiceAction{Key: key, Arg: arg}
}

// classifyMessageAction maps a MessageAction to a translation key. Actions we have no
// dedicated string for fall back to a generic key rather than being dropped, because a
// missing row leaves a visible hole in the conversation and skips a message ID.
func classifyMessageAction(action tg.MessageActionClass, entities entitiesByID) ServiceAction {
	switch a := action.(type) {
	case nil, *tg.MessageActionEmpty:
		return ServiceAction{}
	case *tg.MessageActionChatCreate:
		return withArg(i18n.KeyServiceChatCreated, a.Title)
	case *tg.MessageActionChannelCreate:
		return withArg(i18n.KeyServiceChannelCreated, a.Title)
	case *tg.MessageActionChatEditTitle:
		return withArg(i18n.KeyServiceChatTitleChanged, a.Title)
	case *tg.MessageActionChatEditPhoto:
		return ServiceAction{Key: i18n.KeyServiceChatPhotoChanged}
	case *tg.MessageActionChatDeletePhoto:
		return ServiceAction{Key: i18n.KeyServiceChatPhotoRemoved}
	case *tg.MessageActionChatAddUser:
		return withArg(i18n.KeyServiceUsersAdded, userNames(entities, a.Users))
	case *tg.MessageActionChatDeleteUser:
		return withArg(i18n.KeyServiceUserRemoved, userNames(entities, []int64{a.UserID}))
	case *tg.MessageActionChatJoinedByLink:
		return ServiceAction{Key: i18n.KeyServiceJoinedByLink}
	case *tg.MessageActionChatJoinedByRequest:
		return ServiceAction{Key: i18n.KeyServiceJoinedByRequest}
	case *tg.MessageActionChatMigrateTo:
		return ServiceAction{Key: i18n.KeyServiceMigratedToSupergroup}
	case *tg.MessageActionChannelMigrateFrom:
		return withArg(i18n.KeyServiceMigratedFromGroup, a.Title)
	case *tg.MessageActionPinMessage:
		return ServiceAction{Key: i18n.KeyServicePinnedMessage}
	case *tg.MessageActionHistoryClear:
		return ServiceAction{Key: i18n.KeyServiceHistoryCleared}
	case *tg.MessageActionContactSignUp:
		return ServiceAction{Key: i18n.KeyServiceContactSignUp}
	case *tg.MessageActionScreenshotTaken:
		return ServiceAction{Key: i18n.KeyServiceScreenshotTaken}
	case *tg.MessageActionPhoneCall:
		if d := durationSummary(phoneCallDuration(a)); d != "" {
			return ServiceAction{Key: i18n.KeyServicePhoneCallDuration, Arg: d}
		}
		return ServiceAction{Key: i18n.KeyServicePhoneCall}
	case *tg.MessageActionGroupCall:
		return ServiceAction{Key: i18n.KeyServiceGroupCall}
	case *tg.MessageActionGroupCallScheduled:
		return ServiceAction{Key: i18n.KeyServiceGroupCallScheduled}
	case *tg.MessageActionInviteToGroupCall:
		return withArg(i18n.KeyServiceInviteToGroupCall, userNames(entities, a.Users))
	case *tg.MessageActionGameScore:
		return ServiceAction{Key: i18n.KeyServiceGameScore, Arg: fmt.Sprintf("%d", a.Score)}
	case *tg.MessageActionSetMessagesTTL:
		if d := durationSummary(a.Period); d != "" {
			return ServiceAction{Key: i18n.KeyServiceTTLChanged, Arg: d}
		}
		return ServiceAction{Key: i18n.KeyServiceTTLDisabled}
	case *tg.MessageActionSetChatTheme:
		if emoticon := chatThemeEmoticon(a.Theme); emoticon != "" {
			return ServiceAction{Key: i18n.KeyServiceChatTheme, Arg: emoticon}
		}
		return ServiceAction{Key: i18n.KeyServiceChatThemeRemoved}
	case *tg.MessageActionSetChatWallPaper:
		return ServiceAction{Key: i18n.KeyServiceChatWallpaper}
	case *tg.MessageActionTopicCreate:
		return withArg(i18n.KeyServiceTopicCreated, a.Title)
	case *tg.MessageActionTopicEdit:
		return withArg(i18n.KeyServiceTopicEdited, a.Title)
	case *tg.MessageActionBotAllowed:
		return ServiceAction{Key: i18n.KeyServiceBotAllowed}
	case *tg.MessageActionPaymentSent:
		return withArg(i18n.KeyServicePaymentSent, paymentSummary(a.Currency, a.TotalAmount))
	case *tg.MessageActionGiveawayLaunch:
		return ServiceAction{Key: i18n.KeyServiceGiveawayLaunch}
	case *tg.MessageActionGiveawayResults:
		return ServiceAction{Key: i18n.KeyServiceGiveawayResults}
	case *tg.MessageActionSuggestProfilePhoto:
		return ServiceAction{Key: i18n.KeyServiceSuggestProfilePhoto}
	case *tg.MessageActionCustomAction:
		// Telegram sends this pre-rendered by the server; there is nothing to translate.
		return withArg(i18n.KeyServiceCustom, a.Message)
	default:
		return ServiceAction{Key: i18n.KeyServiceUnsupported}
	}
}

func userNames(entities entitiesByID, ids []int64) string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if u := entities.users[id]; u != nil {
			names = append(names, userTitle(u))
			continue
		}
		names = append(names, fmt.Sprintf("user %d", id))
	}
	return strings.Join(names, ", ")
}

// chatThemeEmoticon extracts the emoji identifying a chat theme. Non-emoji themes
// (unique gifts) have no short label, so they fall back to the "removed" wording.
func chatThemeEmoticon(theme tg.ChatThemeClass) string {
	if t, ok := theme.(*tg.ChatTheme); ok {
		return strings.TrimSpace(t.Emoticon)
	}
	return ""
}

func phoneCallDuration(a *tg.MessageActionPhoneCall) int {
	if a == nil {
		return 0
	}
	if duration, ok := a.GetDuration(); ok {
		return duration
	}
	return 0
}

func durationSummary(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return (time.Duration(seconds) * time.Second).String()
}

func paymentSummary(currency string, amount int64) string {
	if strings.TrimSpace(currency) == "" {
		return ""
	}
	return fmt.Sprintf("%s %d", currency, amount)
}

// normalizeTGServiceMessage converts a service message into the same storage row shape as a
// regular message. Service rows carry no text or media; the UI renders them from
// ServiceKey/ServiceArg.
func (c *GotdClient) normalizeTGServiceMessage(accountID, peerKey string, msg *tg.MessageService, entities entitiesByID) (storage.Message, bool) {
	if msg == nil {
		return storage.Message{}, false
	}
	action := classifyMessageAction(msg.Action, entities)
	if action.Key == "" {
		return storage.Message{}, false
	}
	senderKind, senderID := peerRefParts(msg.FromID)
	senderName := ""
	if senderKind == "user" {
		if u := entities.users[senderID]; u != nil {
			senderName = userTitle(u)
		}
	}
	return storage.Message{
		AccountID:   accountID,
		PeerKey:     peerKey,
		ID:          msg.ID,
		Date:        time.Unix(int64(msg.Date), 0).UTC(),
		Sender:      senderName,
		SenderKind:  senderKind,
		SenderID:    senderID,
		SenderName:  senderName,
		SenderColor: senderColor(senderKind, senderID, senderName),
		Outgoing:    msg.Out,
		State:       "synced",
		ServiceKey:  action.Key,
		ServiceArg:  action.Arg,
	}, true
}

// normalizeUpdateServiceMessage is the service-message counterpart of
// normalizeUpdateMessage: it resolves the peer, refreshes its chat-list activity, and
// produces the storage row.
func (c *GotdClient) normalizeUpdateServiceMessage(ctx context.Context, accountID string, msg *tg.MessageService, e tg.Entities) (storage.Peer, storage.Message, bool) {
	entities := entitiesByID{users: e.Users, chats: e.Chats, channels: e.Channels}
	p, ok := peerFromRef(accountID, serviceMessagePeer(msg), entities)
	if !ok {
		return storage.Peer{}, storage.Message{}, false
	}
	stMsg, ok := c.normalizeTGServiceMessage(accountID, p.Key, msg, entities)
	if !ok {
		return storage.Peer{}, storage.Message{}, false
	}
	p.LastMessageAt = stMsg.Date
	serviceMessagePreview(msg, entities).applyTo(&p)
	p.TopMessageID = msg.ID
	p.UpdatedAt = time.Now().UTC()
	if c.store != nil {
		if existing, ok, err := c.store.Peer(ctx, accountID, p.Key); err == nil && ok {
			p = mergePeerActivity(existing, p)
		}
	}
	return p, stMsg, true
}

func serviceMessagePeer(msg *tg.MessageService) tg.PeerClass {
	if msg == nil {
		return nil
	}
	if msg.PeerID != nil {
		return msg.PeerID
	}
	if from, ok := msg.GetFromID(); ok {
		return from
	}
	return nil
}

// serviceMessagePreview renders the chat-list preview for a service message.
func serviceMessagePreview(msg *tg.MessageService, entities entitiesByID) peerPreview {
	if msg == nil {
		return peerPreview{}
	}
	action := classifyMessageAction(msg.Action, entities)
	if action.Key == "" {
		return peerPreview{}
	}
	return peerPreview{Text: action.Msg().String(), Key: action.Key, Arg: action.Arg}
}
