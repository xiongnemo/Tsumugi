package telegram

import (
	"time"

	"github.com/nemo/Tsumugi/internal/i18n"
)

type EventKind string

const ArchiveFolderID = -10

const (
	EventStatus                  EventKind = "status"
	EventConnected               EventKind = "connected"
	EventChats                   EventKind = "chats"
	EventMessages                EventKind = "messages"
	EventReadOutbox              EventKind = "read_outbox"
	EventAuthPrompt              EventKind = "auth_prompt"
	EventError                   EventKind = "error"
	EventMentionSuggestions      EventKind = "mention_suggestions"
	EventBotCommandSuggestions   EventKind = "bot_command_suggestions"
	EventInlineResultSuggestions EventKind = "inline_result_suggestions"
)

type Event struct {
	Kind      EventKind
	StatusMsg i18n.Msg
	Error     error
	Self      Self
	Chats     []Chat
	Folders   []Folder
	PeerKey   string
	Messages  []Message
	Append    bool
	// Merge adds messages into the existing viewport instead of replacing it.
	Merge bool
	// PreserveViewport keeps message selection and scroll when replacing the list (e.g. after loading older history).
	PreserveViewport bool
	RemoveMessageIDs []string
	ReadOutboxMaxID  int
	Patch            bool
	Auth             *AuthPrompt
	Background       *BackgroundState
	RequestID        int64
	Query            string
	BotUsername      string
	Mentions         []MentionSuggestion
	BotCommands      []BotCommandSuggestion
	InlineResults    []InlineResultSuggestion
	NextOffset       string
	HasMore          bool
	Placeholder      string
}

// BackgroundKind identifies low-priority work shown in the status bar right column.
type BackgroundKind string

const (
	BackgroundIdle     BackgroundKind = ""
	BackgroundDialogs  BackgroundKind = "dialogs"
	BackgroundBackfill BackgroundKind = "backfill"
	BackgroundPaused   BackgroundKind = "paused"
)

type BackgroundState struct {
	Kind    BackgroundKind
	Current int
	Total   int
	Detail  string
}

type AuthPromptKind string

const (
	AuthPromptPhone    AuthPromptKind = "phone"
	AuthPromptCode     AuthPromptKind = "code"
	AuthPromptPassword AuthPromptKind = "password"
)

type AuthPrompt struct {
	Kind      AuthPromptKind
	TitleKey  string
	LabelKey  string
	Secret    bool
	Reply     chan AuthResponse
	HelpMsg   i18n.Msg
	CanCancel bool
}

type AuthResponse struct {
	Value string
	Err   error
}

type Self struct {
	ID       int64
	Name     string
	Username string
	IsBot    bool
}

type Chat struct {
	ID            string
	Title         string
	Subtitle      string
	Kind          string
	Contact       bool
	FolderID      int
	Pinned        bool
	PinnedOrder   int
	Unread        int
	LastPreview   string
	LastMessageAt time.Time
	TopMessageID  int
}

type Folder struct {
	ID      int
	Title   string
	Kind    string
	Archive bool
	Rules   FolderRules
}

type FolderRules struct {
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

type CommandKind string

const (
	CommandOpenChat         CommandKind = "open_chat"
	CommandFocusChat        CommandKind = "focus_chat"
	CommandSendText         CommandKind = "send_text"
	CommandRetrySend        CommandKind = "retry_send"
	CommandLoadOlder        CommandKind = "load_older"
	CommandFillHistoryGap   CommandKind = "fill_history_gap"
	CommandDeleteMessage    CommandKind = "delete_message"
	CommandDownloadMedia    CommandKind = "download_media"
	CommandSendReaction     CommandKind = "send_reaction"
	CommandMarkViewed       CommandKind = "mark_viewed"
	CommandSearchMentions   CommandKind = "search_mentions"
	CommandLoadBotCommands  CommandKind = "load_bot_commands"
	CommandQueryInlineBot   CommandKind = "query_inline_bot"
	CommandSendInlineResult CommandKind = "send_inline_result"
)

type Command struct {
	Kind            CommandKind
	PeerKey         string
	Text            string
	MessageID       int
	ReplyToID       int
	Media           MediaAttachment
	Reaction        ReactionSummary
	RequestID       int64
	Query           string
	BotUsername     string
	Offset          string
	QueryID         int64
	ResultID        string
	MentionEntities []MessageEntityMentionName
}

type MentionSuggestion struct {
	UserID     int64
	AccessHash int64
	Name       string
	Username   string
	IsBot      bool
	Source     string
}

type BotCommandSuggestion struct {
	Command        string
	Description    string
	BotUsername    string
	NeedsBotSuffix bool
}

type InlineResultSuggestion struct {
	ID          string
	QueryID     int64
	Title       string
	Description string
	Type        string
}

type MessageEntityMentionName struct {
	Offset     int
	Length     int
	UserID     int64
	AccessHash int64
}

type Message struct {
	ID             string
	ChatID         string
	Author         string
	AuthorColor    int
	Text           string
	Outgoing       bool
	CreatedAt      time.Time
	Media          MediaAttachment
	ForwardSource  string
	ReplyToID      string
	ReplyToAuthor  string
	State          string
	ReadByPeer     bool
	GroupReadCount int
	Views          int
	Forwards       int
	Reactions      []ReactionSummary
	RecentReact    []ReactionPeer
}

type MediaAttachment struct {
	Kind          string
	LabelKey      string
	Label         string
	FileName      string
	MimeType      string
	Alt           string
	DownloadKey   string
	DocumentID    int64
	AccessHash    int64
	FileReference []byte
	ThumbSize     string
	Size          int64
	Duration      int
	LocalPath     string
	PreviewText   string
}

type Capability string

const (
	CapabilityDialogs     Capability = "dialogs"
	CapabilitySendMessage Capability = "send_message"
	CapabilityMedia       Capability = "media"
	CapabilityBotUpdates  Capability = "bot_updates"
)

type Capabilities map[Capability]bool

func UserCapabilities() Capabilities {
	return Capabilities{
		CapabilityDialogs:     true,
		CapabilitySendMessage: true,
		CapabilityMedia:       true,
	}
}

func BotCapabilities() Capabilities {
	return Capabilities{
		CapabilitySendMessage: true,
		CapabilityMedia:       true,
		CapabilityBotUpdates:  true,
	}
}
