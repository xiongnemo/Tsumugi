package telegram

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	termmedia "github.com/nemo/Tsumugi/internal/media"
	"github.com/nemo/Tsumugi/internal/secure"
	"github.com/nemo/Tsumugi/internal/storage"
)

// sendMediaTestClient builds a client over real storage with the upload replaced, so the whole
// sendMedia path runs without a network: classification, the pending row, the InputMedia, and the
// reconciliation afterwards.
func sendMediaTestClient(t *testing.T) (*GotdClient, *storage.DB, string) {
	t.Helper()
	ctx := context.Background()
	db, _, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := secure.NewCipher(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db.SetCipher(cipher)

	acct := "user:1"
	if err := db.SavePeers(ctx, []storage.Peer{
		{AccountID: acct, Key: "channel:500", Kind: "channel", ID: 500, AccessHash: 77, Title: "Target", LastMessageAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	c := &GotdClient{store: db}
	c.uploadFrom = func(context.Context, termmedia.Outgoing) (tg.InputFileClass, error) {
		return &tg.InputFile{ID: 1, Parts: 1, Name: "upload"}, nil
	}
	// Default to a failing send; the tests that care about success install their own.
	c.mediaSend = func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
		return nil, errors.New("no network in tests")
	}
	return c, db, acct
}

func writeSendable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("not really an image, but it has bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The pending row is what makes a send feel instant, and for media it has to carry the local path:
// that is what lets the picture appear in the chat before the upload has finished.
func TestSendMediaShowsAPendingRowWithTheLocalFile(t *testing.T) {
	c, db, acct := sendMediaTestClient(t)
	path := writeSendable(t, "shot.png")
	events := make(chan Event, 32)

	// The send itself fails at the RPC (no api), which is fine: everything before it is what this
	// test is about, and the failure path is asserted separately.
	c.sendMedia(context.Background(), acct, nil, events, Command{
		Kind: CommandSendMedia, PeerKey: "channel:500", MediaPath: path, Text: "look",
	})

	stored, err := db.MessagesForPeer(context.Background(), acct, "channel:500", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("%d rows stored, want the local echo", len(stored))
	}
	row := stored[0]
	if row.ID >= 0 {
		t.Errorf("row id = %d, want a negative local id", row.ID)
	}
	if row.Text != "look" {
		t.Errorf("Text = %q, want the caption", row.Text)
	}
	if row.MediaKind != string(termmedia.KindPhoto) {
		t.Errorf("MediaKind = %q, want a photo", row.MediaKind)
	}
	if row.MediaJSON == "" {
		t.Fatal("MediaJSON is empty, so the row has no media at all")
	}
	// The local path is the whole point: it survives a failed send so the retry needs no re-pick,
	// and it is what the preview renders from.
	if !bytes.Contains([]byte(row.MediaJSON), []byte("shot.png")) {
		t.Errorf("MediaJSON = %s, want the local file in it", row.MediaJSON)
	}

	got := drainEvents(events)
	if len(got) == 0 {
		t.Fatal("no events at all")
	}
	if got[0].Kind != EventMessages || !got[0].Append || len(got[0].Messages) != 1 {
		t.Fatalf("first event = %+v, want the appended pending row", got[0])
	}
}

// A failed send must leave a row that can be retried, not vanish. The file is still on disk; losing
// the row would mean re-picking it.
func TestSendMediaLeavesAFailedRowWithItsPath(t *testing.T) {
	c, db, acct := sendMediaTestClient(t)
	path := writeSendable(t, "clip.mp4")
	events := make(chan Event, 32)

	c.sendMedia(context.Background(), acct, nil, events, Command{
		Kind: CommandSendMedia, PeerKey: "channel:500", MediaPath: path,
	})

	stored, err := db.MessagesForPeer(context.Background(), acct, "channel:500", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].State != "failed" {
		t.Fatalf("stored = %+v, want one failed row", stored)
	}
	if !bytes.Contains([]byte(stored[0].MediaJSON), []byte("clip.mp4")) {
		t.Fatal("the failed row lost its local path, so it cannot be retried")
	}
}

// The success path: the request carries what it should, and the local row is replaced by the server's
// message rather than joining it. Two rows for one message is the failure this whole echo mechanism
// exists to prevent.
func TestSendMediaReplacesThePendingRowOnSuccess(t *testing.T) {
	c, db, acct := sendMediaTestClient(t)
	path := writeSendable(t, "shot.png")
	events := make(chan Event, 32)

	var got *tg.MessagesSendMediaRequest
	c.mediaSend = func(_ context.Context, req *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
		got = req
		return &tg.Updates{Updates: []tg.UpdateClass{
			&tg.UpdateNewChannelMessage{Message: &tg.Message{
				ID:     4242,
				Date:   int(time.Now().Unix()),
				Out:    true,
				PeerID: &tg.PeerChannel{ChannelID: 500},
			}},
		}}, nil
	}

	c.sendMedia(context.Background(), acct, nil, events, Command{
		Kind: CommandSendMedia, PeerKey: "channel:500", MediaPath: path, Text: "caption", ReplyToID: 11,
	})

	if got == nil {
		t.Fatal("no send request was made")
	}
	if got.Message != "caption" {
		t.Errorf("Message = %q, want the composer text as the caption", got.Message)
	}
	if got.RandomID == 0 {
		t.Error("RandomID = 0; Telegram drops duplicates silently without one")
	}
	if _, ok := got.Media.(*tg.InputMediaUploadedPhoto); !ok {
		t.Errorf("Media = %T, want an uploaded photo", got.Media)
	}
	if reply, ok := got.ReplyTo.(*tg.InputReplyToMessage); !ok || reply.ReplyToMsgID != 11 {
		t.Errorf("ReplyTo = %+v, want message 11", got.ReplyTo)
	}

	stored, err := db.MessagesForPeer(context.Background(), acct, "channel:500", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("%d rows stored, want only the server message: %+v", len(stored), stored)
	}
	if stored[0].ID != 4242 || stored[0].State != "synced" {
		t.Fatalf("row = id %d state %q, want the synced server message", stored[0].ID, stored[0].State)
	}

	var removed bool
	for _, event := range drainEvents(events) {
		if len(event.RemoveMessageIDs) > 0 {
			removed = true
		}
	}
	if !removed {
		t.Error("no event told the viewport to drop the pending row")
	}
}

func TestSendMediaRefusesWhatCannotBeSent(t *testing.T) {
	c, db, acct := sendMediaTestClient(t)
	events := make(chan Event, 8)

	c.sendMedia(context.Background(), acct, nil, events, Command{
		Kind: CommandSendMedia, PeerKey: "channel:500", MediaPath: filepath.Join(t.TempDir(), "gone.png"),
	})

	stored, err := db.MessagesForPeer(context.Background(), acct, "channel:500", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("%d rows stored for a file that does not exist", len(stored))
	}
	got := drainEvents(events)
	if len(got) != 1 || got[0].Error == nil {
		t.Fatalf("events = %+v, want a single error", got)
	}
}

// A photo goes as a photo; a document keeps the original bytes. Getting this backwards silently
// re-encodes whatever the user was trying to preserve.
func TestOutgoingInputMediaShape(t *testing.T) {
	uploaded := &tg.InputFile{ID: 7}

	photo, err := termmedia.InspectOutgoing(writeSendable(t, "shot.png"), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := outgoingInputMedia(photo, uploaded).(*tg.InputMediaUploadedPhoto); !ok {
		t.Fatalf("a png became %T, want an uploaded photo", outgoingInputMedia(photo, uploaded))
	}

	asFile, err := termmedia.InspectOutgoing(writeSendable(t, "shot.png"), true)
	if err != nil {
		t.Fatal(err)
	}
	document, ok := outgoingInputMedia(asFile, uploaded).(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("send-as-file became %T, want an uploaded document", outgoingInputMedia(asFile, uploaded))
	}
	if document.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want the real type even as a document", document.MimeType)
	}
	// The filename attribute is what the recipient sees; without it the file arrives unnamed.
	var named bool
	for _, attr := range document.Attributes {
		if name, ok := attr.(*tg.DocumentAttributeFilename); ok && name.FileName == "shot.png" {
			named = true
		}
	}
	if !named {
		t.Error("the document carries no filename attribute")
	}
}

// An animation is a document with the animated attribute. As a photo it would be flattened to a
// single frame, silently.
func TestOutgoingInputMediaKeepsGIFsAnimated(t *testing.T) {
	gif, err := termmedia.InspectOutgoing(writeSendable(t, "loop.gif"), false)
	if err != nil {
		t.Fatal(err)
	}
	document, ok := outgoingInputMedia(gif, &tg.InputFile{}).(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatal("a gif was not sent as a document")
	}
	var animated bool
	for _, attr := range document.Attributes {
		if _, ok := attr.(*tg.DocumentAttributeAnimated); ok {
			animated = true
		}
	}
	if !animated {
		t.Error("the gif carries no animated attribute, so it arrives as a still")
	}
}

// Without ffprobe there are no dimensions, and a video attribute full of zeros gives the recipient a
// player that cannot seek. No attribute at all is the better answer.
func TestOutgoingInputMediaOmitsVideoAttributesWithoutAProbe(t *testing.T) {
	if termmedia.FFprobeAvailable() {
		t.Skip("ffprobe is installed, so the degraded path cannot be observed here")
	}
	video, err := termmedia.InspectOutgoing(writeSendable(t, "clip.mp4"), false)
	if err != nil {
		t.Fatal(err)
	}
	document, ok := outgoingInputMedia(video, &tg.InputFile{}).(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatal("a video was not sent as a document")
	}
	for _, attr := range document.Attributes {
		if _, ok := attr.(*tg.DocumentAttributeVideo); ok {
			t.Fatal("a video attribute was attached with nothing to fill it")
		}
	}
}

func TestUploadPercentIsClamped(t *testing.T) {
	cases := []struct {
		done, total int64
		want        int
	}{
		{0, 100, 0},
		{50, 100, 50},
		{100, 100, 100},
		// The final part reports its padded size, which can exceed the file length.
		{110, 100, 100},
		{50, 0, 0},
	}
	for _, tc := range cases {
		if got := uploadPercent(tc.done, tc.total); got != tc.want {
			t.Errorf("uploadPercent(%d, %d) = %d, want %d", tc.done, tc.total, got, tc.want)
		}
	}
}
