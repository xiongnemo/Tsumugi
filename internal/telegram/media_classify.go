package telegram

import (
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
)

func mediaAttachment(kind, labelKey string) MediaAttachment {
	return MediaAttachment{Kind: kind, LabelKey: labelKey, Label: i18n.T(labelKey)}
}

func classifyMessageMedia(media tg.MessageMediaClass) MediaAttachment {
	if media == nil {
		return MediaAttachment{}
	}
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		if photo, ok := m.Photo.(*tg.Photo); ok {
			return classifyPhoto(photo)
		}
		return mediaAttachment("photo", i18n.KeyMediaPhoto)
	case *tg.MessageMediaDocument:
		if doc, ok := m.Document.(*tg.Document); ok {
			return classifyDocument(doc)
		}
		return mediaAttachment("document", i18n.KeyMediaDocument)
	case *tg.MessageMediaPoll:
		return mediaAttachment("poll", i18n.KeyMediaPoll)
	case *tg.MessageMediaContact:
		return mediaAttachment("contact", i18n.KeyMediaContact)
	case *tg.MessageMediaGeo:
		return mediaAttachment("location", i18n.KeyMediaLocation)
	case *tg.MessageMediaGeoLive:
		return mediaAttachment("live_location", i18n.KeyMediaLiveLocation)
	case *tg.MessageMediaVenue:
		return mediaAttachment("venue", i18n.KeyMediaVenue)
	case *tg.MessageMediaWebPage:
		return mediaAttachment("link", i18n.KeyMediaLink)
	case *tg.MessageMediaDice:
		return mediaAttachment("dice", i18n.KeyMediaDice)
	case *tg.MessageMediaGame:
		return mediaAttachment("game", i18n.KeyMediaGame)
	case *tg.MessageMediaInvoice:
		return mediaAttachment("invoice", i18n.KeyMediaInvoice)
	case *tg.MessageMediaStory:
		return mediaAttachment("story", i18n.KeyMediaStory)
	case *tg.MessageMediaGiveaway:
		return mediaAttachment("giveaway", i18n.KeyMediaGiveaway)
	case *tg.MessageMediaGiveawayResults:
		return mediaAttachment("giveaway_results", i18n.KeyMediaGiveawayResults)
	case *tg.MessageMediaPaidMedia:
		return mediaAttachment("paid_media", i18n.KeyMediaPaidMedia)
	case *tg.MessageMediaToDo:
		return mediaAttachment("todo", i18n.KeyMediaTodo)
	case *tg.MessageMediaVideoStream:
		return mediaAttachment("live_stream", i18n.KeyMediaLiveStream)
	case *tg.MessageMediaEmpty:
		return MediaAttachment{}
	case *tg.MessageMediaUnsupported:
		return mediaAttachment("unsupported", i18n.KeyMediaUnsupported)
	default:
		return mediaAttachment("unsupported", i18n.KeyMediaUnsupported)
	}
}

// LocalizeMediaAttachment refreshes the visible label from LabelKey using the current locale.
func LocalizeMediaAttachment(media MediaAttachment) MediaAttachment {
	if media.LabelKey != "" {
		media.Label = i18n.T(media.LabelKey)
	} else if media.Kind != "" {
		media.Label = i18n.MediaLabel("", media.Kind)
	}
	return media
}

func classifyPhoto(photo *tg.Photo) MediaAttachment {
	attachment := MediaAttachment{
		Kind:          "photo",
		LabelKey:      i18n.KeyMediaPhoto,
		Label:         i18n.T(i18n.KeyMediaPhoto),
		FileName:      "photo.jpg",
		MimeType:      "image/jpeg",
		DownloadKey:   fmt.Sprintf("photo:%d", photo.ID),
		DocumentID:    photo.ID,
		AccessHash:    photo.AccessHash,
		FileReference: append([]byte(nil), photo.FileReference...),
	}
	if thumb := bestPhotoSize(photo.Sizes); thumb != "" {
		attachment.ThumbSize = thumb
	}
	return attachment
}

func classifyDocument(doc *tg.Document) MediaAttachment {
	attachment := MediaAttachment{
		Kind:          "document",
		LabelKey:      i18n.KeyMediaDocument,
		Label:         i18n.T(i18n.KeyMediaDocument),
		MimeType:      doc.MimeType,
		DownloadKey:   fmt.Sprintf("document:%d", doc.ID),
		DocumentID:    doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: append([]byte(nil), doc.FileReference...),
		Size:          doc.Size,
	}
	var fileName, alt string
	sticker, animated, video, voice, audio := false, false, false, false, false
	for _, attr := range doc.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeFilename:
			fileName = a.FileName
		case *tg.DocumentAttributeSticker:
			sticker = true
			alt = a.Alt
		case *tg.DocumentAttributeAnimated:
			animated = true
		case *tg.DocumentAttributeVideo:
			video = true
			attachment.Duration = int(a.Duration)
		case *tg.DocumentAttributeAudio:
			audio = true
			voice = a.Voice
			attachment.Duration = int(a.Duration)
		}
	}
	attachment.FileName = fileName
	attachment.Alt = alt
	switch {
	case sticker && doc.MimeType == "application/x-tgsticker":
		attachment.Kind = "animated_sticker"
		attachment.LabelKey = i18n.KeyMediaAnimatedSticker
		attachment.Label = stickerLabelLocalized(i18n.KeyMediaAnimatedSticker, alt)
	case sticker && doc.MimeType == "video/webm":
		attachment.Kind = "video_sticker"
		attachment.LabelKey = i18n.KeyMediaVideoSticker
		attachment.Label = stickerLabelLocalized(i18n.KeyMediaVideoSticker, alt)
	case sticker:
		attachment.Kind = "sticker"
		attachment.LabelKey = i18n.KeyMediaSticker
		attachment.Label = stickerLabelLocalized(i18n.KeyMediaSticker, alt)
	case animated || doc.MimeType == "image/gif":
		attachment.Kind = "gif"
		attachment.LabelKey = i18n.KeyMediaGIF
		attachment.Label = i18n.T(i18n.KeyMediaGIF)
	case video || doc.MimeType == "video/mp4" || doc.MimeType == "video/webm":
		attachment.Kind = "video"
		attachment.LabelKey = i18n.KeyMediaVideo
		attachment.Label = i18n.T(i18n.KeyMediaVideo)
	case voice:
		attachment.Kind = "voice"
		attachment.LabelKey = i18n.KeyMediaVoice
		attachment.Label = i18n.T(i18n.KeyMediaVoice)
	case audio:
		attachment.Kind = "audio"
		attachment.LabelKey = i18n.KeyMediaAudio
		attachment.Label = i18n.T(i18n.KeyMediaAudio)
	case len(doc.Thumbs) > 0:
		attachment.LabelKey = i18n.KeyMediaDocumentThumb
		attachment.Label = i18n.T(i18n.KeyMediaDocumentThumb)
	}
	if thumb := bestPhotoSize(doc.Thumbs); thumb != "" {
		attachment.ThumbSize = thumb
	}
	return attachment
}

func stickerLabelLocalized(labelKey, alt string) string {
	base := i18n.T(labelKey)
	if alt == "" {
		return base
	}
	return base + " " + alt
}
