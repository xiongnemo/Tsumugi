package telegram

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/debuglog"
	"github.com/nemo/Tsumugi/internal/i18n"
	termmedia "github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/storage"
)

// PreviewIsRaster reports whether preview text is terminal raster/ANSI art.
func PreviewIsRaster(text string) bool {
	return strings.Contains(text, "\x1b[") || strings.Contains(text, "[#") || strings.Contains(text, "[rgb:")
}

func hydrateMediaPreviewFromDisk(mediaDir string, media MediaAttachment) MediaAttachment {
	media = resolveAnimatedLocalPath(mediaDir, media)
	if PreviewIsRaster(media.PreviewText) {
		return media
	}
	for _, path := range candidatePreviewPaths(mediaDir, media) {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			continue
		}
		raster := renderMediaPreview(path)
		if raster == "" {
			raster = termmedia.StillPreviewANSIToTview(path, termmedia.PreviewMaxCols, termmedia.PreviewMaxRows)
		}
		if raster == "" {
			continue
		}
		// #region agent log
		debuglog.Log("H7-H8", "media_hydrate.go:hydrate", "disk preview hit", map[string]any{
			"kind":   media.Kind,
			"path":   filepath.Base(path),
			"runId":  "media-preview-post",
		})
		// #endregion
		media.LocalPath = path
		media.PreviewText = raster
		return media
	}
	// #region agent log
	debuglog.Log("H7-H9", "media_hydrate.go:hydrate", "no disk preview", map[string]any{
		"kind":      media.Kind,
		"mime":      media.MimeType,
		"localPath": filepath.Base(media.LocalPath),
		"runId":     "media-preview-post",
	})
	// #endregion
	return media
}

func candidatePreviewPaths(mediaDir string, media MediaAttachment) []string {
	if media.DownloadKey == "" {
		return nil
	}
	base := safeFileName(media.DownloadKey)
	seen := map[string]struct{}{}
	var paths []string
	add := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	add(media.LocalPath)
	if media.ThumbSize != "" {
		add(filepath.Join(mediaDir, base+"-"+safeFileName(media.ThumbSize)+".jpg"))
	}
	switch media.Kind {
	case "sticker", "animated_sticker":
		add(filepath.Join(mediaDir, base+mediaExtension(media)))
		add(filepath.Join(mediaDir, base+".webp"))
	case "video_sticker", "gif":
		add(filepath.Join(mediaDir, base+animatedDocumentExt(media)))
	default:
		add(filepath.Join(mediaDir, base+mediaExtension(media)))
	}
	return paths
}

const mediaPreviewPatchBatch = 3

func (c *GotdClient) enrichPeerMessagePreviews(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, peerKey string, msgs []storage.Message) {
	if c.store == nil || len(msgs) == 0 {
		return
	}
	if !c.isFocusedPeer(peerKey) {
		return
	}
	needCount := 0
	for _, st := range msgs {
		if st.MediaJSON == "" {
			continue
		}
		var media MediaAttachment
		if err := json.Unmarshal([]byte(st.MediaJSON), &media); err != nil {
			continue
		}
		if !PreviewIsRaster(media.PreviewText) {
			needCount++
		}
	}
	if needCount == 0 {
		return
	}
	c.sendFocusedEvent(ctx, events, peerKey, Event{
		Kind:      EventStatus,
		StatusMsg: i18n.M(i18n.KeyStatusLoadingMediaPreviews, 0, needCount),
	})
	// #region agent log
	debuglog.Log("H10", "media_hydrate.go:enrichPeerMessagePreviews", "enrich start", map[string]any{
		"peerKey":   peerKey,
		"needCount": needCount,
		"runId":     "media-preview-post",
	})
	// #endregion

	patches := make([]Message, 0, mediaPreviewPatchBatch)
	updated := make([]storage.Message, 0, mediaPreviewPatchBatch)
	doneCount := 0
	flush := func(forceSave bool) {
		if len(patches) == 0 {
			return
		}
		if forceSave && len(updated) > 0 {
			_ = c.store.SaveMessages(ctx, updated)
			updated = updated[:0]
		}
		// #region agent log
		debuglog.Log("H10", "media_hydrate.go:enrichPeerMessagePreviews", "patch previews", map[string]any{
			"peerKey":    peerKey,
			"patchCount": len(patches),
			"doneCount":  doneCount,
			"needCount":  needCount,
			"runId":      "media-preview-post",
		})
		// #endregion
		c.sendFocusedEvent(ctx, events, peerKey, Event{
			Kind:      EventStatus,
			StatusMsg: i18n.M(i18n.KeyStatusLoadingMediaPreviews, doneCount, needCount),
		})
		c.sendFocusedEvent(ctx, events, peerKey, Event{
			Kind:     EventMessages,
			PeerKey:  peerKey,
			Messages: append([]Message(nil), patches...),
			Patch:    true,
		})
		patches = patches[:0]
	}
	for _, st := range msgs {
		if !c.isFocusedPeer(peerKey) {
			return
		}
		if st.MediaJSON == "" {
			continue
		}
		var media MediaAttachment
		if err := json.Unmarshal([]byte(st.MediaJSON), &media); err != nil {
			continue
		}
		if PreviewIsRaster(media.PreviewText) {
			continue
		}
		media = LocalizeMediaAttachment(media)
		media = c.enrichStoredMediaPreview(ctx, api, accountID, peerKey, st.ID, media)
		media = hydrateMediaPreviewFromDisk(c.cfg.Paths.MediaDir, media)
		if !PreviewIsRaster(media.PreviewText) {
			// #region agent log
			debuglog.Log("H11", "media_hydrate.go:enrichPeerMessagePreviews", "enrich skipped", map[string]any{
				"peerKey":     peerKey,
				"messageID":   st.ID,
				"kind":        media.Kind,
				"downloadKey": media.DownloadKey,
				"runId":       "media-preview-post",
			})
			// #endregion
			continue
		}
		if raw, err := json.Marshal(media); err == nil {
			st.MediaJSON = string(raw)
			updated = append(updated, st)
		}
		tm := toTelegramMessages([]storage.Message{st})[0]
		tm.Media = media
		patches = append(patches, tm)
		doneCount++
		if len(patches) >= mediaPreviewPatchBatch {
			flush(true)
		}
	}
	flush(true)
	if !c.isFocusedPeer(peerKey) {
		return
	}
	c.sendFocusedEvent(ctx, events, peerKey, Event{
		Kind:      EventStatus,
		StatusMsg: i18n.M(i18n.KeyStatusMediaPreviewsReady, doneCount),
	})
	// #region agent log
	debuglog.Log("H10", "media_hydrate.go:enrichPeerMessagePreviews", "enrich done", map[string]any{
		"peerKey":   peerKey,
		"doneCount": doneCount,
		"needCount": needCount,
		"runId":     "media-preview-post",
	})
	// #endregion
}
