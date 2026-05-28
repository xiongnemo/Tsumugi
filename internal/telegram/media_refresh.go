package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/debuglog"
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
		// #region agent log
		debuglog.Log("H11", "media_refresh.go:refreshMessageMedia", "fetch message failed", map[string]any{
			"peerKey":   peerKey,
			"messageID": messageID,
			"err":       err.Error(),
			"runId":     "media-preview-post",
		})
		// #endregion
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
	// #region agent log
	debuglog.Log("H11", "media_refresh.go:refreshMessageMedia", "file ref refreshed", map[string]any{
		"peerKey":   peerKey,
		"messageID": messageID,
		"kind":      media.Kind,
		"runId":     "media-preview-post",
	})
	// #endregion
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
		// #region agent log
		debuglog.Log("H8", "client.go:enrichMediaPreview", "preview unavailable", map[string]any{
			"kind":  media.Kind,
			"err":   err.Error(),
			"runId": "media-preview-post",
		})
		// #endregion
		media.PreviewText = mediaFallbackText(media) + " (preview unavailable)"
		media = hydrateMediaPreviewFromDisk(c.cfg.Paths.MediaDir, media)
		return media, isFileReferenceExpired(err)
	}
	if localPath != "" {
		media.LocalPath = localPath
		media.PreviewText = renderMediaPreview(localPath)
		if media.PreviewText == "" {
			media.PreviewText = termmedia.StillPreviewANSIToTview(localPath, termmedia.PreviewMaxCols, termmedia.PreviewMaxRows)
		}
		if media.PreviewText == "" {
			media.PreviewText = mediaFallbackText(media) + " (cached; press O to open)"
		}
	}
	// #region agent log
	debuglog.Log("H8", "client.go:enrichMediaPreview", "preview enriched", map[string]any{
		"kind":       media.Kind,
		"raster":     PreviewIsRaster(media.PreviewText),
		"previewLen": len(media.PreviewText),
		"runId":      "media-preview-post",
	})
	// #endregion
	return media, false
}
