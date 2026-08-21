package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
	termmedia "github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/storage"
)

// uploadProgressInterval is how often an upload reports itself.
//
// The uploader calls back once per part, which on a large file is hundreds of times a second: every
// one of those would be a QueueUpdateDraw, and the redraw storm would cost more than the upload.
const uploadProgressInterval = 500 * time.Millisecond

// sendMedia uploads a local file and sends it, with the composer text as its caption.
//
// The shape mirrors sendText: a pending row appears first so the chat never looks like nothing
// happened, then the real message replaces it. The difference is that the middle step can take
// minutes, which is why this must be dispatched with `go` and why it reports progress.
func (c *GotdClient) sendMedia(ctx context.Context, accountID string, api *tg.Client, events chan<- Event, command Command) {
	if c.store == nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: fmt.Errorf("storage is unavailable")})
		return
	}
	peerKey := command.PeerKey
	file, err := termmedia.InspectOutgoing(command.MediaPath, command.MediaAsFile)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	p, ok, err := c.store.Peer(ctx, accountID, peerKey)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("peer %s not found", peerKey)
		}
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	input, err := inputPeer(p)
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}
	randomID, err := randomInt64()
	if err != nil {
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
		return
	}

	caption := command.Text
	pendingID := -int(randomID & 0x7fffffff)
	pending := storage.Message{
		AccountID:   accountID,
		PeerKey:     peerKey,
		ID:          pendingID,
		Date:        time.Now().UTC(),
		Sender:      "me",
		SenderName:  "me",
		SenderColor: senderColor("self", 0, "me"),
		Outgoing:    true,
		Text:        caption,
		ReplyToID:   command.ReplyToID,
		State:       "pending",
		MediaKind:   file.Kind,
	}
	attachment := outgoingAttachment(file)
	if raw, err := json.Marshal(attachment); err == nil {
		pending.MediaJSON = string(raw)
	}
	_ = c.store.SaveMessages(ctx, []storage.Message{pending})
	// Both matchers: the identity one always works, and the text one covers a captioned send whose
	// updateMessageID never arrives. Registering both is safe because handle() tries identity first
	// and only falls back when it misses.
	c.rememberPendingEcho(peerKey, pendingID, randomID)
	if caption != "" {
		c.rememberPending(peerKey, caption, pendingID)
	}
	sendEvent(ctx, events, Event{
		Kind:      EventMessages,
		PeerKey:   peerKey,
		Messages:  c.telegramMessages(ctx, accountID, []storage.Message{pending}),
		Append:    true,
		StatusMsg: i18n.M(i18n.KeyStatusUploading, file.FileName, 0),
	})

	fail := func(err error) {
		c.forgetPendingEcho(randomID)
		if caption != "" {
			c.forgetPending(peerKey, caption, pendingID)
		}
		// The row keeps its MediaJSON, so LocalPath survives and the send can be retried from the
		// same file rather than being re-picked.
		pending.State = "failed"
		_ = c.store.SaveMessages(ctx, []storage.Message{pending})
		sendEvent(ctx, events, Event{Kind: EventMessages, PeerKey: peerKey, Messages: c.telegramMessages(ctx, accountID, []storage.Message{pending}), Append: true})
		sendEvent(ctx, events, Event{Kind: EventError, Error: err})
	}

	uploaded, err := c.uploadFile(ctx, api, events, file)
	if err != nil {
		fail(fmt.Errorf("upload %s: %w", file.FileName, err))
		return
	}

	request := &tg.MessagesSendMediaRequest{
		Peer:     input,
		Media:    outgoingInputMedia(file, uploaded),
		Message:  caption,
		RandomID: randomID,
	}
	if command.ReplyToID != 0 {
		request.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: command.ReplyToID}
	}
	if entities := buildMentionNameEntities(command.MentionEntities); len(entities) > 0 {
		request.SetEntities(entities)
	}
	send := c.mediaSend
	if send == nil {
		send = api.MessagesSendMedia
	}
	updates, err := retryFloodWait(ctx, defaultMaxFloodWaits, "sending media", func(ctx context.Context) (tg.UpdatesClass, error) {
		return send(ctx, request)
	})
	if err != nil {
		fail(rpcError("send media", err))
		return
	}

	serverMessages := c.messagesFromSendUpdates(ctx, api, accountID, peerKey, caption, command.ReplyToID, updates)
	if len(serverMessages) == 0 {
		// The message is on its way; the update stream will bring it and claim the pending row by
		// its random id.
		sendEvent(ctx, events, Event{Kind: EventStatus, StatusMsg: i18n.M(i18n.KeyStatusMessageSubmitted)})
		return
	}
	c.forgetPendingEcho(randomID)
	if caption != "" {
		c.forgetPending(peerKey, caption, pendingID)
	}
	_ = c.store.DeleteMessage(ctx, accountID, peerKey, pendingID)
	_ = c.store.SaveMessages(ctx, serverMessages)
	sendEvent(ctx, events, Event{
		Kind:             EventMessages,
		PeerKey:          peerKey,
		Messages:         c.telegramMessages(ctx, accountID, serverMessages),
		Append:           true,
		RemoveMessageIDs: intIDsToStrings([]int{pendingID}),
		StatusMsg:        i18n.M(i18n.KeyStatusMessageSent),
	})
	c.clearDraftAfterSend(ctx, accountID, api, peerKey)
}

// uploadFile puts the bytes on Telegram's servers, reporting progress as it goes.
//
// gotd's uploader owns the part sizing, the small/big split and the parallel parts. Hand-rolling
// upload.saveBigFilePart would be a worse copy of code that is already in the module.
func (c *GotdClient) uploadFile(ctx context.Context, api *tg.Client, events chan<- Event, file termmedia.Outgoing) (tg.InputFileClass, error) {
	if c.uploadFrom != nil {
		return c.uploadFrom(ctx, file)
	}
	progress := &uploadProgress{name: file.FileName, total: file.Size, ctx: ctx, events: events}
	return uploader.NewUploader(api).WithProgress(progress).FromPath(ctx, file.Path)
}

// uploadProgress turns per-part callbacks into an occasional status line.
type uploadProgress struct {
	name   string
	total  int64
	ctx    context.Context
	events chan<- Event
	last   time.Time
}

func (p *uploadProgress) Chunk(_ context.Context, state uploader.ProgressState) error {
	if time.Since(p.last) < uploadProgressInterval {
		return nil
	}
	p.last = time.Now()
	total := p.total
	if state.Total > 0 {
		total = state.Total
	}
	sendEvent(p.ctx, p.events, Event{
		Kind:      EventStatus,
		StatusMsg: i18n.M(i18n.KeyStatusUploading, p.name, uploadPercent(state.Uploaded, total)),
	})
	return nil
}

// uploadPercent is clamped rather than trusted: the last part reports the padded part size, which
// can push the sum past the file length and show 103%.
func uploadPercent(done, total int64) int {
	if total <= 0 {
		return 0
	}
	percent := int(done * 100 / total)
	if percent > 100 {
		return 100
	}
	if percent < 0 {
		return 0
	}
	return percent
}

// outgoingAttachment describes the pending row's media, so it renders like a received message.
func outgoingAttachment(file termmedia.Outgoing) MediaAttachment {
	attachment := MediaAttachment{
		Kind:      file.Kind,
		FileName:  file.FileName,
		MimeType:  file.MimeType,
		Size:      file.Size,
		LocalPath: file.Path,
	}
	attachment.LabelKey = outgoingLabelKey(file.Kind)
	attachment.Label = i18n.T(attachment.LabelKey)
	// The image the user just picked, shown before the upload finishes. Nothing to download and
	// nothing to wait for: the file is on this machine.
	if file.Kind == string(termmedia.KindPhoto) || file.Kind == string(termmedia.KindGIFAnimation) {
		attachment.PreviewText = renderMediaPreview(file.Path)
	}
	if attachment.PreviewText == "" {
		attachment.PreviewText = mediaFallbackText(attachment)
	}
	return attachment
}

func outgoingLabelKey(kind string) string {
	switch kind {
	case string(termmedia.KindPhoto):
		return i18n.KeyMediaPhoto
	case string(termmedia.KindGIFAnimation):
		return i18n.KeyMediaGIF
	case string(termmedia.KindVideo):
		return i18n.KeyMediaVideo
	case "audio":
		return i18n.KeyMediaAudio
	default:
		return i18n.KeyMediaDocument
	}
}

// outgoingInputMedia builds the InputMedia for an uploaded file.
//
// Video and audio attributes are attached only when ffprobe could fill them in. ffmpeg is optional
// here, and a video attribute carrying zeros is worse than no attribute at all: the recipient gets a
// player that cannot seek instead of a file they can open.
func outgoingInputMedia(file termmedia.Outgoing, uploaded tg.InputFileClass) tg.InputMediaClass {
	if file.AsPhoto {
		return &tg.InputMediaUploadedPhoto{File: uploaded}
	}
	document := &tg.InputMediaUploadedDocument{
		File:     uploaded,
		MimeType: file.MimeType,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: file.FileName},
		},
	}
	switch file.Kind {
	case string(termmedia.KindGIFAnimation):
		document.Attributes = append(document.Attributes, &tg.DocumentAttributeAnimated{})
		if probe := termmedia.ProbeMedia(file.Path); probe.Ok && probe.Width > 0 {
			document.Attributes = append(document.Attributes, &tg.DocumentAttributeVideo{
				W: probe.Width, H: probe.Height, Duration: probe.Duration,
			})
		}
	case string(termmedia.KindVideo):
		if probe := termmedia.ProbeMedia(file.Path); probe.Ok && probe.Width > 0 {
			document.Attributes = append(document.Attributes, &tg.DocumentAttributeVideo{
				W: probe.Width, H: probe.Height, Duration: probe.Duration,
				SupportsStreaming: true,
			})
		}
	case "audio":
		if probe := termmedia.ProbeMedia(file.Path); probe.Ok && probe.Duration > 0 {
			document.Attributes = append(document.Attributes, &tg.DocumentAttributeAudio{
				Duration: int(probe.Duration),
			})
		}
	}
	return document
}
