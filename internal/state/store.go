package state

import (
	"sort"
	"sync"

	"github.com/nemo/Tsumugi/internal/telegram"
)

type Store struct {
	mu       sync.RWMutex
	self     telegram.Self
	status   string
	chats    map[string]telegram.Chat
	messages map[string][]telegram.Message
}

func NewStore() *Store {
	return &Store{
		chats:    make(map[string]telegram.Chat),
		messages: make(map[string][]telegram.Message),
	}
}

func (s *Store) Apply(event telegram.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !event.StatusMsg.IsZero() {
		s.status = event.StatusMsg.String()
	}
	if event.Self.ID != 0 || event.Self.Name != "" {
		s.self = event.Self
	}
	for _, chat := range event.Chats {
		s.chats[chat.ID] = chat
	}
	for _, message := range event.Messages {
		list := s.messages[message.ChatID]
		replaced := false
		for i := range list {
			if list[i].ID == message.ID {
				list[i] = message
				replaced = true
				break
			}
		}
		if !replaced {
			list = append(list, message)
		}
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].CreatedAt.Equal(list[j].CreatedAt) {
				return list[i].ID < list[j].ID
			}
			return list[i].CreatedAt.Before(list[j].CreatedAt)
		})
		s.messages[message.ChatID] = list
	}
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	chats := make([]telegram.Chat, 0, len(s.chats))
	for _, chat := range s.chats {
		chats = append(chats, chat)
	}
	sort.SliceStable(chats, func(i, j int) bool {
		if chats[i].Pinned != chats[j].Pinned {
			return chats[i].Pinned
		}
		if chats[i].Pinned && chats[j].Pinned && chats[i].PinnedOrder != chats[j].PinnedOrder {
			return chats[i].PinnedOrder < chats[j].PinnedOrder
		}
		if !chats[i].LastMessageAt.Equal(chats[j].LastMessageAt) {
			return chats[i].LastMessageAt.After(chats[j].LastMessageAt)
		}
		return chats[i].Title < chats[j].Title
	})

	messages := make(map[string][]telegram.Message, len(s.messages))
	for chatID, list := range s.messages {
		messages[chatID] = append([]telegram.Message(nil), list...)
	}

	return Snapshot{
		Self:     s.self,
		Status:   s.status,
		Chats:    chats,
		Messages: messages,
	}
}

type Snapshot struct {
	Self     telegram.Self
	Status   string
	Chats    []telegram.Chat
	Messages map[string][]telegram.Message
}
