package telegram

import (
	"fmt"
	"strconv"
	"strings"
)

func IsBroadcastChannel(kind, subtitle string) bool {
	return kind == "channel" && subtitle == "channel"
}

func ChatIsBroadcast(chat Chat) bool {
	if chat.Kind != "channel" {
		return false
	}
	sub := chat.Subtitle
	if i := strings.Index(sub, " | "); i >= 0 {
		sub = sub[:i]
	}
	return sub == "channel"
}

func ChatSupportsGroupReadMarks(chat Chat) bool {
	sub := chat.Subtitle
	if i := strings.Index(sub, " | "); i >= 0 {
		sub = sub[:i]
	}
	return chat.Kind == "chat" || (chat.Kind == "channel" && sub == "group")
}

func FormatViewCount(n int) string {
	if n <= 0 {
		return ""
	}
	switch {
	case n >= 1_000_000:
		v := float64(n) / 1_000_000
		if v >= 10 {
			return fmt.Sprintf("%.0fm", v)
		}
		return fmt.Sprintf("%.1fm", v)
	case n >= 1_000:
		v := float64(n) / 1_000
		if v >= 10 {
			return fmt.Sprintf("%.0fk", v)
		}
		return fmt.Sprintf("%.1fk", v)
	default:
		return strconv.Itoa(n)
	}
}

func intMessageIDs(messages []Message) []int {
	out := make([]int, 0, len(messages))
	for _, msg := range messages {
		id, err := strconv.Atoi(msg.ID)
		if err != nil || id <= 0 {
			continue
		}
		out = append(out, id)
	}
	return out
}

func applyViewsMap(messages []Message, views map[int]messageViewCounts) []Message {
	if len(views) == 0 {
		return messages
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		id, err := strconv.Atoi(out[i].ID)
		if err != nil {
			continue
		}
		v, ok := views[id]
		if !ok {
			continue
		}
		out[i].Views = v.Views
		out[i].Forwards = v.Forwards
	}
	return out
}

type messageViewCounts struct {
	Views    int
	Forwards int
}

func patchMessageViews(messages []Message, msgID, views int) []Message {
	if views <= 0 {
		return messages
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	idStr := strconv.Itoa(msgID)
	for i := range out {
		if out[i].ID == idStr {
			out[i].Views = views
			break
		}
	}
	return out
}
