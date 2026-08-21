package telegram

import (
	"testing"
	"time"
)

// The whole point of the identity path: a message with no text has no text key, so this is the case
// the old matching could not handle at all. Getting it wrong shows the message twice with no way to
// tell which row is real.
func TestPendingEchoResolvesWithoutAnyText(t *testing.T) {
	c := &GotdClient{}
	const randomID = int64(0x5eed)
	c.rememberPendingEcho("chat:1", -42, randomID)

	if _, ok := c.takePendingEchoForServerID("chat:1", 1001); ok {
		t.Fatal("claimed a server id before updateMessageID mapped it")
	}
	c.resolveEchoRandomID(randomID, 1001)

	localID, ok := c.takePendingEchoForServerID("chat:1", 1001)
	if !ok || localID != -42 {
		t.Fatalf("claim = (%d, %v), want the pending row -42", localID, ok)
	}
}

// Claiming is destructive: the same message reaches the UI through both the send result and the
// update stream, and only the first of them may delete the local row. A second claim removing a
// second row would delete an unrelated pending message.
func TestPendingEchoIsClaimedOnce(t *testing.T) {
	c := &GotdClient{}
	c.rememberPendingEcho("chat:1", -7, 99)
	c.resolveEchoRandomID(99, 500)

	if _, ok := c.takePendingEchoForServerID("chat:1", 500); !ok {
		t.Fatal("first claim failed")
	}
	if _, ok := c.takePendingEchoForServerID("chat:1", 500); ok {
		t.Fatal("second claim succeeded; the local row would be deleted twice")
	}
}

// Message ids are only unique inside a peer. Without the peer check, id 500 arriving in another chat
// would delete this chat's pending row.
func TestPendingEchoRequiresTheSamePeer(t *testing.T) {
	c := &GotdClient{}
	c.rememberPendingEcho("chat:1", -7, 99)
	c.resolveEchoRandomID(99, 500)

	if _, ok := c.takePendingEchoForServerID("chat:2", 500); ok {
		t.Fatal("claimed a pending row from a different chat")
	}
	if _, ok := c.takePendingEchoForServerID("chat:1", 500); !ok {
		t.Fatal("the rightful peer could no longer claim it")
	}
}

// Messages from our other sessions carry random ids we never generated. Those must be ignored, not
// mistaken for a local echo.
func TestPendingEchoIgnoresUnknownRandomIDs(t *testing.T) {
	c := &GotdClient{}
	c.resolveEchoRandomID(12345, 900)

	if _, ok := c.takePendingEchoForServerID("chat:1", 900); ok {
		t.Fatal("an unknown random id produced a claim")
	}
}

func TestPendingEchoForgottenOnFailure(t *testing.T) {
	c := &GotdClient{}
	c.rememberPendingEcho("chat:1", -7, 99)
	c.forgetPendingEcho(99)
	c.resolveEchoRandomID(99, 500)

	if _, ok := c.takePendingEchoForServerID("chat:1", 500); ok {
		t.Fatal("a failed send still claimed a server id")
	}
}

// Unclaimed entries must not accumulate for the life of the session, and an ancient one must not
// claim a row it has nothing to do with.
func TestPendingEchoExpires(t *testing.T) {
	c := &GotdClient{}
	c.rememberPendingEcho("chat:1", -7, 99)

	c.echoMu.Lock()
	echo := c.echoByRandom[99]
	echo.CreatedAt = time.Now().UTC().Add(-2 * pendingEchoTTL)
	c.echoByRandom[99] = echo
	c.echoMu.Unlock()

	c.resolveEchoRandomID(99, 500)
	if _, ok := c.takePendingEchoForServerID("chat:1", 500); ok {
		t.Fatal("an expired entry was still claimable")
	}
	c.echoMu.Lock()
	remaining := len(c.echoByRandom) + len(c.echoByServer)
	c.echoMu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d entries left after expiry, want none", remaining)
	}
}
