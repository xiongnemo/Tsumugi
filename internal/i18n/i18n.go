package i18n

import (
	"strings"
	"sync"
)

var (
	mu     sync.RWMutex
	locale = "en"
)

func SetLocale(code string) {
	mu.Lock()
	defer mu.Unlock()
	switch normalizeLocale(code) {
	case "zh":
		locale = "zh"
	default:
		locale = "en"
	}
}

func Locale() string {
	mu.RLock()
	defer mu.RUnlock()
	return locale
}

func T(key string) string {
	mu.RLock()
	loc := locale
	mu.RUnlock()
	table := tableFor(loc)
	if value, ok := table[key]; ok {
		return value
	}
	if value, ok := catalogEN[key]; ok {
		return value
	}
	return key
}

func MediaLabel(labelKey, kind string) string {
	if labelKey != "" {
		return T(labelKey)
	}
	switch kind {
	case "photo":
		return T(KeyMediaPhoto)
	case "gif":
		return T(KeyMediaGIF)
	case "video":
		return T(KeyMediaVideo)
	case "voice":
		return T(KeyMediaVoice)
	case "audio":
		return T(KeyMediaAudio)
	case "sticker", "animated_sticker", "video_sticker":
		return T(KeyMediaSticker)
	case "poll":
		return T(KeyMediaPoll)
	default:
		if kind != "" {
			return T(KeyMediaDocument)
		}
		return T(KeyMediaGeneric)
	}
}

func normalizeLocale(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return "en"
	}
	if strings.HasPrefix(code, "zh") {
		return "zh"
	}
	if strings.HasPrefix(code, "en") {
		return "en"
	}
	return code
}

func ChatKind(subtitle string) string {
	switch strings.ToLower(strings.TrimSpace(subtitle)) {
	case "private":
		return T(KeyUIChatPrivate)
	case "group":
		return T(KeyUIChatGroup)
	case "channel":
		return T(KeyUIChatChannel)
	case "forbidden group":
		return T(KeyUIChatForbiddenGroup)
	case "forbidden channel":
		return T(KeyUIChatForbiddenChannel)
	default:
		return subtitle
	}
}

func FolderTitle(title string) string {
	if strings.EqualFold(strings.TrimSpace(title), "all") {
		return T(KeyUIFolderAll)
	}
	return title
}

func LocalizeKnown(text string) string {
	if text == "" {
		return text
	}
	if key := knownKeyForText(text); key != "" {
		return T(key)
	}
	return text
}
