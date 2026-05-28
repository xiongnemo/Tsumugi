package media

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gotd/td/tg"
)

type Kind string

const (
	KindNone            Kind = ""
	KindPhoto           Kind = "photo"
	KindDocument        Kind = "document"
	KindSticker         Kind = "sticker"
	KindAnimatedSticker Kind = "animated_sticker"
	KindVideoSticker    Kind = "video_sticker"
	KindGIFAnimation    Kind = "gif"
	KindVideo           Kind = "video"
)

type Attributes struct {
	MimeType string
	FileName string
	Alt      string
	Sticker  bool
	Animated bool
	Video    bool
}

type Attachment struct {
	Kind     string
	Label    string
	FileName string
	MimeType string
	Alt      string
}

func Classify(attrs Attributes) Attachment {
	kind := KindDocument
	label := "[Document]"

	switch {
	case attrs.Sticker && attrs.MimeType == "application/x-tgsticker":
		kind = KindAnimatedSticker
		label = stickerLabel("Animated sticker", attrs.Alt)
	case attrs.Sticker && attrs.MimeType == "video/webm":
		kind = KindVideoSticker
		label = stickerLabel("Video sticker", attrs.Alt)
	case attrs.Sticker:
		kind = KindSticker
		label = stickerLabel("Sticker", attrs.Alt)
	case attrs.Animated:
		kind = KindGIFAnimation
		label = "[GIF]"
	case strings.HasPrefix(attrs.MimeType, "video/") || attrs.Video:
		kind = KindVideo
		label = "[Video]"
	case strings.HasPrefix(attrs.MimeType, "image/"):
		kind = KindPhoto
		label = "[Photo]"
	case attrs.FileName != "":
		label = fmt.Sprintf("[Document: %s]", attrs.FileName)
	}

	return Attachment{
		Kind:     string(kind),
		Label:    label,
		FileName: attrs.FileName,
		MimeType: attrs.MimeType,
		Alt:      attrs.Alt,
	}
}

func ClassifyDocument(doc *tg.Document) Attachment {
	if doc == nil {
		return Attachment{}
	}
	attrs := Attributes{MimeType: doc.MimeType}
	for _, attr := range doc.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeSticker:
			attrs.Sticker = true
			attrs.Alt = a.Alt
		case *tg.DocumentAttributeAnimated:
			attrs.Animated = true
		case *tg.DocumentAttributeVideo:
			attrs.Video = true
		case *tg.DocumentAttributeFilename:
			attrs.FileName = a.FileName
		}
	}
	return Classify(attrs)
}

func CacheName(messageID, fileName, mimeType string) string {
	if fileName == "" {
		fileName = defaultName(mimeType)
	}
	base := filepath.Base(fileName)
	if messageID == "" {
		return base
	}
	return sanitize(messageID) + "-" + sanitize(base)
}

func stickerLabel(prefix, alt string) string {
	if alt == "" {
		return "[" + prefix + "]"
	}
	return "[" + prefix + " " + alt + "]"
}

func defaultName(mimeType string) string {
	switch mimeType {
	case "image/webp":
		return "sticker.webp"
	case "application/x-tgsticker":
		return "sticker.tgs"
	case "video/webm":
		return "video.webm"
	case "video/mp4":
		return "video.mp4"
	default:
		return "media.bin"
	}
}

func sanitize(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
