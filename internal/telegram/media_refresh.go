package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"

	termmedia "github.com/nemo/Tsumugi/internal/media"
)

func isFileReferenceExpired(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "FILE_REFERENCE_EXPIRED")
}

func tgMessageByID(modified tg.MessagesMessagesClass, messageID int) (*tg.Message, bool) {
	switch m := modified.(type) {
	case *tg.MessagesMessages:
		for _, item := range m.Messages {
			if msg, ok := item.(*tg.Message); ok && msg.ID == messageID {
				return msg, true
			}
		}
	case *tg.MessagesChannelMessages:
		for _, item := range m.Messages {
			if msg, ok := item.(*tg.Message); ok && msg.ID == messageID {
				return msg, true
			}
		}
	}
	return nil, false
}

func (c *GotdClient) refreshMessageMedia(ctx context.Context, api *tg.Client, accountID, peerKey string, messageID int) (MediaAttachment, bool) {
	if c.store == nil || api == nil || messageID <= 0 {
		return MediaAttachment{}, false
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		return MediaAttachment{}, false
	}
	msgID := []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}}
	var modified tg.MessagesMessagesClass
	switch p.Kind {
	case "channel":
		modified, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: p.ID, AccessHash: p.AccessHash},
			ID:      msgID,
		})
	default:
		modified, err = api.MessagesGetMessages(ctx, msgID)
	}
	if err != nil {
		return MediaAttachment{}, false
	}
	msg, ok := tgMessageByID(modified, messageID)
	if !ok || msg == nil {
		return MediaAttachment{}, false
	}
	media := classifyMessageMedia(msg.Media)
	if media.Kind == "" {
		return MediaAttachment{}, false
	}
	return media, true
}

func (c *GotdClient) enrichStoredMediaPreview(ctx context.Context, api *tg.Client, accountID, peerKey string, messageID int, media MediaAttachment) MediaAttachment {
	if media.Kind == "" {
		return media
	}
	media = LocalizeMediaAttachment(media)
	media, expired := c.enrichMediaPreviewAttempt(ctx, api, media)
	if PreviewIsRaster(media.PreviewText) {
		return media
	}
	if !expired || api == nil || messageID <= 0 {
		return media
	}
	fresh, ok := c.refreshMessageMedia(ctx, api, accountID, peerKey, messageID)
	if !ok || fresh.DownloadKey == "" {
		return media
	}
	fresh = LocalizeMediaAttachment(fresh)
	refreshed, _ := c.enrichMediaPreviewAttempt(ctx, api, fresh)
	if PreviewIsRaster(refreshed.PreviewText) {
		return refreshed
	}
	return refreshed
}

func (c *GotdClient) enrichMediaPreviewAttempt(ctx context.Context, api *tg.Client, media MediaAttachment) (MediaAttachment, bool) {
	if media.Kind == "" {
		return media, false
	}
	if media.Duration > 0 && media.Label != "" && (media.Kind == "gif" || media.Kind == "video") {
		media.Label = fmt.Sprintf("%s %ds", media.Label, media.Duration)
	}
	if api == nil || media.DownloadKey == "" || media.DocumentID == 0 {
		media.PreviewText = mediaFallbackText(media)
		return media, false
	}
	localPath, err := c.ensureMediaPreview(ctx, api, media)
	if err != nil {
		media.PreviewText = mediaFallbackText(media) + " (preview unavailable)"
		media = hydrateMediaPreviewFromDisk(c.cfg.Paths.MediaDir, media)
		return media, isFileReferenceExpired(err)
	}
	if localPath != "" {
		media.LocalPath = localPath
		media.PreviewText = renderMediaPreview(localPath)
		if media.PreviewText == "" {
			media.PreviewText = renderVideoStillPreview(localPath, termmedia.PreviewMaxCols, termmedia.PreviewMaxRows)
		}
		if media.PreviewText == "" {
			media.PreviewText = c.thumbnailPreview(ctx, api, media)
		}
		if media.PreviewText == "" {
			media.PreviewText = mediaFallbackText(media) + " (cached; press O to open)"
		}
	}
	return media, false
}

// thumbnailPreview renders Telegram's own JPEG thumbnail for the attachment.
//
// This is the fallback for GIFs and video stickers, whose cached file is an MP4: the pure Go
// still decoder cannot read it, and the frame-extraction path needs ffmpeg in PATH. Without
// this step a machine with no ffmpeg showed those messages as a bare "[GIF] 3s" label with no
// picture at all, which is much worse than a static frame. Plain videos already resolve to
// their thumbnail in ensureMediaPreview and never reach here.
//
// LocalPath is deliberately left pointing at the animation, because that is what O opens and
// what the inline animator decodes; only the drawn raster comes from the thumbnail.
func (c *GotdClient) thumbnailPreview(ctx context.Context, api *tg.Client, media MediaAttachment) string {
	if api == nil || media.ThumbSize == "" {
		return ""
	}
	thumbPath, err := c.ensureDocumentThumb(ctx, api, media)
	if err != nil || thumbPath == "" {
		return ""
	}
	return renderMediaPreview(thumbPath)
}
