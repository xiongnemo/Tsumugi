package telegram

import (
	"strconv"

	"github.com/nemo/Tsumugi/internal/storage"
)

func peerSupportsOutboxRead(peer storage.Peer) bool {
	return peer.Kind == "user" || peer.Kind == "self"
}

func applyOutboxRead(messages []Message, peer storage.Peer) {
	if !peerSupportsOutboxRead(peer) || peer.ReadOutboxMaxID <= 0 {
		return
	}
	for i := range messages {
		if !messages[i].Outgoing || messages[i].State != "synced" {
			continue
		}
		id, err := strconv.Atoi(messages[i].ID)
		if err != nil || id <= 0 {
			continue
		}
		messages[i].ReadByPeer = id <= peer.ReadOutboxMaxID
	}
}

func applyOutboxReadMaxID(messages []Message, maxID int) {
	if maxID <= 0 {
		return
	}
	for i := range messages {
		if !messages[i].Outgoing || messages[i].State != "synced" {
			continue
		}
		id, err := strconv.Atoi(messages[i].ID)
		if err != nil || id <= 0 {
			continue
		}
		if id <= maxID {
			messages[i].ReadByPeer = true
		}
	}
}
