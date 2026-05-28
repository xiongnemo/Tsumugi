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
	Key                string
	AccountID          string
	Kind               string
	ID                 int64
	AccessHash         int64
	Title              string
	Username           string
	Subtitle           string
	Contact            bool
	LastPreview        string
	LastMessageAt      time.Time
	TopMessageID       int
	FolderID           int
	FolderTitle        string
	Pinned             bool
	PinnedOrder        int
	Unread             int
	ReadOutboxMaxID    int
	HistoryMinID       int
	HistoryLoadedUntil time.Time
	ThumbCacheKey      string
	UpdatedAt          time.Time
}

type Message struct {
	AccountID     string
	PeerKey       string
	ID            int
	Date          time.Time
	Sender        string
	SenderKind    string
	SenderID      int64
	SenderName    string
	SenderColor   int
	Outgoing      bool
	Text          string
	MediaJSON     string
	MediaKind     string
	ForwardSource string
	ReplyToID     int
	State         string
	Views         int
	Forwards      int
	ReactionsJSON string
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
