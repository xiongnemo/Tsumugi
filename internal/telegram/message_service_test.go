package telegram

import (
	"strings"
	"testing"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/storage"
)

func init() {
	i18n.SetLocale("en")
}

func testEntities() entitiesByID {
	return entitiesByID{
		users: map[int64]*tg.User{
			7:  {ID: 7, FirstName: "Nemo"},
			8:  {ID: 8, Username: "saltedfish"},
			99: {ID: 99, FirstName: "Ada", LastName: "Lovelace"},
		},
		chats:    map[int64]*tg.Chat{},
		channels: map[int64]*tg.Channel{},
	}
}

// Every service template either takes exactly one %s and is always given a non-empty
// argument, or takes none and is never given one. Breaking this pairing renders a
// literal "%s" or a "%!(EXTRA string=...)" suffix in the conversation.
func TestServiceActionArgMatchesTemplate(t *testing.T) {
	entities := testEntities()
	actions := []tg.MessageActionClass{
		&tg.MessageActionChatCreate{Title: "Nemo group", Users: []int64{7}},
		&tg.MessageActionChatCreate{}, // missing title -> generic key
		&tg.MessageActionChannelCreate{Title: "Broadcast"},
		&tg.MessageActionChatEditTitle{Title: "New title"},
		&tg.MessageActionChatEditPhoto{},
		&tg.MessageActionChatDeletePhoto{},
		&tg.MessageActionChatAddUser{Users: []int64{7, 8}},
		&tg.MessageActionChatDeleteUser{UserID: 99},
		&tg.MessageActionChatJoinedByLink{},
		&tg.MessageActionChatJoinedByRequest{},
		&tg.MessageActionChatMigrateTo{},
		&tg.MessageActionChannelMigrateFrom{Title: "Old group"},
		&tg.MessageActionPinMessage{},
		&tg.MessageActionHistoryClear{},
		&tg.MessageActionContactSignUp{},
		&tg.MessageActionScreenshotTaken{},
		&tg.MessageActionPhoneCall{},
		&tg.MessageActionGroupCall{},
		&tg.MessageActionGroupCallScheduled{},
		&tg.MessageActionInviteToGroupCall{Users: []int64{7}},
		&tg.MessageActionGameScore{Score: 42},
		&tg.MessageActionSetMessagesTTL{Period: 0},
		&tg.MessageActionSetMessagesTTL{Period: 86400},
		&tg.MessageActionSetChatTheme{},
		&tg.MessageActionSetChatTheme{Theme: &tg.ChatTheme{Emoticon: "🌙"}},
		&tg.MessageActionSetChatWallPaper{},
		&tg.MessageActionTopicCreate{Title: "General"},
		&tg.MessageActionTopicEdit{Title: "Renamed"},
		&tg.MessageActionBotAllowed{},
		&tg.MessageActionPaymentSent{Currency: "USD", TotalAmount: 500},
		&tg.MessageActionPaymentSent{},
		&tg.MessageActionGiveawayLaunch{},
		&tg.MessageActionGiveawayResults{},
		&tg.MessageActionSuggestProfilePhoto{},
		&tg.MessageActionCustomAction{Message: "Server said so"},
		&tg.MessageActionCustomAction{},
		&tg.MessageActionBoostApply{}, // unmapped -> generic key
	}

	for _, action := range actions {
		got := classifyMessageAction(action, entities)
		if got.Key == "" {
			t.Fatalf("%T: classified to an empty key", action)
		}
		for _, locale := range []string{"en", "zh"} {
			i18n.SetLocale(locale)
			template := i18n.T(got.Key)
			if template == "" || template == got.Key {
				t.Fatalf("%T: key %q has no %s translation", action, got.Key, locale)
			}
			wantArg := strings.Contains(template, "%s")
			if wantArg != (got.Arg != "") {
				t.Fatalf("%T: key %q template %q (%s) wantArg=%v, got Arg=%q",
					action, got.Key, template, locale, wantArg, got.Arg)
			}
			rendered := got.Msg().String()
			if strings.Contains(rendered, "%!") || strings.Contains(rendered, "%s") {
				t.Fatalf("%T: rendered %q in %s", action, rendered, locale)
			}
		}
		i18n.SetLocale("en")
	}
}

func TestClassifyMessageActionResolvesUserNames(t *testing.T) {
	entities := testEntities()

	added := classifyMessageAction(&tg.MessageActionChatAddUser{Users: []int64{7, 99}}, entities)
	if added.Key != i18n.KeyServiceUsersAdded {
		t.Fatalf("key = %q", added.Key)
	}
	if added.Arg != "Nemo, Ada Lovelace" {
		t.Fatalf("arg = %q", added.Arg)
	}

	// Users missing from the update entities still render as a stable placeholder.
	unknown := classifyMessageAction(&tg.MessageActionChatDeleteUser{UserID: 4242}, entities)
	if unknown.Arg != "user 4242" {
		t.Fatalf("arg = %q", unknown.Arg)
	}

	// Username-only users fall back to @handle.
	handle := classifyMessageAction(&tg.MessageActionChatAddUser{Users: []int64{8}}, entities)
	if handle.Arg != "@saltedfish" {
		t.Fatalf("arg = %q", handle.Arg)
	}
}

func TestClassifyMessageActionEmptyIsSkipped(t *testing.T) {
	if got := classifyMessageAction(&tg.MessageActionEmpty{}, testEntities()); got.Key != "" {
		t.Fatalf("MessageActionEmpty classified to %q, want skip", got.Key)
	}
	if got := classifyMessageAction(nil, testEntities()); got.Key != "" {
		t.Fatalf("nil action classified to %q, want skip", got.Key)
	}
}

func TestNormalizeTGServiceMessage(t *testing.T) {
	client := &GotdClient{}
	msg := &tg.MessageService{
		ID:     512,
		Date:   1700000000,
		PeerID: &tg.PeerChat{ChatID: 3},
		Action: &tg.MessageActionPinMessage{},
	}
	msg.SetFromID(&tg.PeerUser{UserID: 7})

	got, ok := client.normalizeTGServiceMessage("user:1", "chat:3", msg, testEntities())
	if !ok {
		t.Fatal("normalizeTGServiceMessage returned !ok")
	}
	if got.ID != 512 || got.PeerKey != "chat:3" {
		t.Fatalf("id/peer = %d/%q", got.ID, got.PeerKey)
	}
	if got.ServiceKey != i18n.KeyServicePinnedMessage {
		t.Fatalf("service key = %q", got.ServiceKey)
	}
	if got.SenderName != "Nemo" {
		t.Fatalf("sender = %q", got.SenderName)
	}
	if got.Text != "" || got.MediaKind != "" {
		t.Fatalf("service row should carry no text/media, got %q/%q", got.Text, got.MediaKind)
	}

	// An empty action is not a renderable row and must not occupy the viewport.
	if _, ok := client.normalizeTGServiceMessage("user:1", "chat:3", &tg.MessageService{
		ID:     513,
		PeerID: &tg.PeerChat{ChatID: 3},
		Action: &tg.MessageActionEmpty{},
	}, testEntities()); ok {
		t.Fatal("empty action produced a storage row")
	}
}

func TestChatSubtitleIsBareKind(t *testing.T) {
	// i18n.ChatKind matches by exact string and folder rules compare with ==, so the
	// subtitle must stay a bare kind token even when a preview exists.
	got := chatSubtitle(storage.Peer{Kind: "channel", Subtitle: "group", LastPreview: "[Photo]"})
	if got != "group" {
		t.Fatalf("chatSubtitle = %q, want %q", got, "group")
	}
	if fallback := chatSubtitle(storage.Peer{Kind: "channel"}); fallback != "channel" {
		t.Fatalf("chatSubtitle fallback = %q, want kind", fallback)
	}
}

func TestServiceMessageCarriesPinnedReplyTarget(t *testing.T) {
	client := &GotdClient{}
	msg := &tg.MessageService{
		ID:     900,
		Date:   1700000000,
		PeerID: &tg.PeerChat{ChatID: 3},
		Action: &tg.MessageActionPinMessage{},
	}
	msg.SetReplyTo(&tg.MessageReplyHeader{ReplyToMsgID: 512})

	got, ok := client.normalizeTGServiceMessage("user:1", "chat:3", msg, testEntities())
	if !ok {
		t.Fatal("normalizeTGServiceMessage returned !ok")
	}
	// Without this the UI cannot offer "jump to pinned message" from the system row.
	if got.ReplyToID != 512 {
		t.Fatalf("ReplyToID = %d, want 512", got.ReplyToID)
	}
}
