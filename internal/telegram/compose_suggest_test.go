package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestBotCommandsFromInfoMarksGroupCommandsWithBotSuffix(t *testing.T) {
	got := botCommandsFromInfo([]tg.BotInfo{
		botInfoWithCommands(42, []tg.BotCommand{{Command: "ping", Description: "Pong"}}),
	}, map[int64]string{42: "anzupop_bot"}, true)

	if len(got) != 1 {
		t.Fatalf("commands = %+v, want one", got)
	}
	if !got[0].NeedsBotSuffix {
		t.Fatalf("NeedsBotSuffix = false, want true")
	}
	if got[0].BotUsername != "anzupop_bot" {
		t.Fatalf("BotUsername = %q, want anzupop_bot", got[0].BotUsername)
	}
}

func TestBotCommandsFromInfoPrivateChatDoesNotForceBotSuffix(t *testing.T) {
	got := botCommandsFromInfo([]tg.BotInfo{
		botInfoWithCommands(42, []tg.BotCommand{{Command: "ping", Description: "Pong"}}),
	}, map[int64]string{42: "anzupop_bot"}, false)

	if len(got) != 1 {
		t.Fatalf("commands = %+v, want one", got)
	}
	if got[0].NeedsBotSuffix {
		t.Fatalf("NeedsBotSuffix = true, want false")
	}
}

func botInfoWithCommands(userID int64, commands []tg.BotCommand) tg.BotInfo {
	info := tg.BotInfo{UserID: userID}
	info.SetCommands(commands)
	return info
}
