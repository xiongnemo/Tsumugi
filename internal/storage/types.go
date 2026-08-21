package storage

import (
	"time"

	"github.com/nemo/Tsumugi/internal/network"
)

type Account struct {
	ID          string
	Mode        string
	DisplayName string
	Username    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Peer struct {
	Key         string
	AccountID   string
	Kind        string
	ID          int64
	AccessHash  int64
	Title       string
	Username    string
	Subtitle    string
	Contact     bool
	LastPreview string
	// LastPreviewKey is an i18n key when the preview is generated text (a media
	// placeholder, an empty-message marker, or a service message). It is empty when
	// LastPreview holds user-authored message text, which is never translated.
	LastPreviewKey  string
	LastPreviewArg  string
	LastMessageAt   time.Time
	TopMessageID    int
	FolderID        int
	FolderTitle     string
	Pinned          bool
	PinnedOrder     int
	Unread          int
	ReadOutboxMaxID int
	// ReadInboxMaxID is the highest incoming message we have read. The first unread message
	// is the next one after it, which is what the jump-on-open anchors to.
	ReadInboxMaxID     int
	HistoryMinID       int
	HistoryLoadedUntil time.Time
	ThumbCacheKey      string
	UpdatedAt          time.Time
	// MuteUntil is read-only here: it lives in peer_mutes and is filled in by ListPeers and Peer.
	// SavePeers must never write it - that upsert runs on every incoming message, and only a dialog
	// sync carries notify settings, so writing it would blank the mute. See mutes.go.
	MuteUntil int
}

type Message struct {
	AccountID      string
	PeerKey        string
	ID             int
	Date           time.Time
	Sender         string
	SenderKind     string
	SenderID       int64
	SenderName     string
	SenderColor    int
	Outgoing       bool
	Text           string
	MediaJSON      string
	MediaKind      string
	ForwardSource  string
	ReplyToID      int
	State          string
	Views          int
	Forwards       int
	ReactionsJSON  string
	ViaBotUsername string
	// ServiceKey is an i18n key when this row is a Telegram service (system) message,
	// empty for ordinary messages. ServiceArg is its single template argument.
	ServiceKey string
	ServiceArg string
}

// Draft is a per-peer unsent message. Text is sealed like message bodies because it is user
// content. Dirty marks a local edit the server has not accepted yet, which is what stops the
// repeating background dialog sweep from overwriting something still being typed.
type Draft struct {
	AccountID    string
	PeerKey      string
	Text         string
	ReplyToID    int
	EntitiesJSON string
	ServerDate   int
	Dirty        bool
	UpdatedAt    time.Time
}

type DialogFilter struct {
	AccountID       string
	ID              int
	Title           string
	Kind            string
	Archive         bool
	Contacts        bool
	NonContacts     bool
	Groups          bool
	Broadcasts      bool
	Bots            bool
	ExcludeMuted    bool
	ExcludeRead     bool
	ExcludeArchived bool
	IncludePeers    []string
	ExcludePeers    []string
	PinnedPeers     []string
}

type UpdateState struct {
	AccountID string
	Pts       int
	Qts       int
	Date      int
	Seq       int
	UpdatedAt time.Time
}

type ProxyProfile struct {
	ID        int64
	Name      string
	Config    network.ProxyConfig
	Active    bool
	ReadOnly  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}
