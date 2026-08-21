package telegram

import (
	"time"
)

// A local echo is the row Tsumugi shows the instant you press send, before Telegram has the
// message. It has to be removed exactly once, when the server's version of that same message
// arrives — and the server's version can arrive by two routes that race each other: the reply to
// our own send RPC, or the update stream.
//
// The text path matches those two rows by their text (pendingKey). That cannot work for media: a
// photo with no caption has no text, and takeMatchingPending gives up on an empty string. When the
// update stream wins the race the pending row is never claimed, so the chat shows the message
// twice, forever, with no way to tell which one is real.
//
// updateMessageID is Telegram's own answer to this. It carries the random_id we generated and the
// message id the server assigned, and it is delivered ahead of the message itself in the same
// updates container. Registering it turns "guess by content" into an exact identity mapping.
//
// So there are two hops, and an entry moves from one map to the other:
//
//	send            -> echoByRandom[random_id] = local row
//	updateMessageID -> echoByServer[server_id] = local row
//	the message     -> claim echoByServer[server_id], delete the local row
//
// The text matching stays as a fallback: rows written before this existed, and sends whose
// updateMessageID never shows up, still reconcile the old way.

// pendingEchoTTL is how long an unclaimed entry is kept. The same five minutes the text-keyed map
// uses; past that the send is either lost or has been reconciled some other way, and holding the
// entry would only risk claiming an unrelated row.
const pendingEchoTTL = 5 * time.Minute

// pendingEcho is a local row waiting for the server's version of the same message.
type pendingEcho struct {
	PeerKey   string
	LocalID   int
	CreatedAt time.Time
}

// rememberPendingEcho records that a local row is waiting for the message we just sent with
// randomID.
func (c *GotdClient) rememberPendingEcho(peerKey string, localID int, randomID int64) {
	if randomID == 0 || localID == 0 {
		return
	}
	c.echoMu.Lock()
	defer c.echoMu.Unlock()
	if c.echoByRandom == nil {
		c.echoByRandom = make(map[int64]pendingEcho)
	}
	c.pruneEchoesLocked()
	c.echoByRandom[randomID] = pendingEcho{PeerKey: peerKey, LocalID: localID, CreatedAt: time.Now().UTC()}
}

// resolveEchoRandomID re-keys a waiting entry from our random id to the server's message id.
//
// Called from the updateMessageID handler. Unknown random ids are ignored: they belong to another
// session of the same account, whose messages we have no local row for.
func (c *GotdClient) resolveEchoRandomID(randomID int64, serverID int) {
	if randomID == 0 || serverID == 0 {
		return
	}
	c.echoMu.Lock()
	defer c.echoMu.Unlock()
	c.pruneEchoesLocked()
	echo, ok := c.echoByRandom[randomID]
	if !ok {
		return
	}
	delete(c.echoByRandom, randomID)
	if c.echoByServer == nil {
		c.echoByServer = make(map[int]pendingEcho)
	}
	c.echoByServer[serverID] = echo
}

// takePendingEchoForServerID claims the local row that the arriving server message replaces.
//
// Claiming is destructive on purpose: the same message can reach the UI through both the send
// result and the update stream, and only the first of them may remove the local row.
func (c *GotdClient) takePendingEchoForServerID(peerKey string, serverID int) (int, bool) {
	if serverID == 0 {
		return 0, false
	}
	c.echoMu.Lock()
	defer c.echoMu.Unlock()
	c.pruneEchoesLocked()
	echo, ok := c.echoByServer[serverID]
	if !ok || echo.PeerKey != peerKey {
		// A server id is only unique within a peer, so the peer has to match. Without the check a
		// message id reused in another chat could delete an unrelated pending row.
		return 0, false
	}
	delete(c.echoByServer, serverID)
	return echo.LocalID, true
}

// forgetPendingEcho drops a waiting entry, for a send that failed outright.
func (c *GotdClient) forgetPendingEcho(randomID int64) {
	if randomID == 0 {
		return
	}
	c.echoMu.Lock()
	defer c.echoMu.Unlock()
	delete(c.echoByRandom, randomID)
	c.pruneEchoesLocked()
}

// pruneEchoesLocked drops entries nothing is going to claim. Called from every access rather than
// on a timer: these maps are only touched while sending or receiving, so there is no idle case to
// worry about, and a session that never sends never allocates.
func (c *GotdClient) pruneEchoesLocked() {
	cutoff := time.Now().UTC().Add(-pendingEchoTTL)
	for randomID, echo := range c.echoByRandom {
		if echo.CreatedAt.Before(cutoff) {
			delete(c.echoByRandom, randomID)
		}
	}
	for serverID, echo := range c.echoByServer {
		if echo.CreatedAt.Before(cutoff) {
			delete(c.echoByServer, serverID)
		}
	}
}
