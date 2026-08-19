package telegram

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gotd/td/tgerr"
)

// The status bar's middle column truncates at 56 runes. gotd's own text reads
// "rpc error code 400: CHAT_FORWARDS_RESTRICTED (0)", and behind an operation prefix the error
// type — the only actionable part — is exactly what falls off the end.
func TestDescribeRPCErrorLeadsWithTheType(t *testing.T) {
	err := &tgerr.Error{Code: 400, Message: "CHAT_FORWARDS_RESTRICTED", Type: "CHAT_FORWARDS_RESTRICTED"}

	got := describeRPCError(err)

	if !strings.HasPrefix(got, "CHAT_FORWARDS_RESTRICTED") {
		t.Fatalf("describeRPCError = %q, want the error type first", got)
	}
	if !strings.Contains(got, "400") {
		t.Fatalf("describeRPCError = %q, want the code kept", got)
	}
}

// The whole point is that it survives the status bar. Verify the rendered line fits.
func TestRPCErrorFitsTheStatusColumn(t *testing.T) {
	err := &tgerr.Error{Code: 400, Message: "CHAT_FORWARDS_RESTRICTED", Type: "CHAT_FORWARDS_RESTRICTED"}

	// "[red]" markup plus the operation prefix, which is what the UI actually renders.
	rendered := "[red]" + rpcError("forward messages", err).Error()

	if n := len([]rune(rendered)); n > 56 {
		t.Fatalf("rendered error is %d runes (%q), which the 56-rune status column would truncate", n, rendered)
	}
	if !strings.Contains(rendered, "CHAT_FORWARDS_RESTRICTED") {
		t.Fatalf("rendered = %q, want the error type visible", rendered)
	}
}

// FLOOD_WAIT carries its seconds in the argument; dropping that loses the useful half.
func TestDescribeRPCErrorKeepsTheArgument(t *testing.T) {
	err := &tgerr.Error{Code: 420, Message: "FLOOD_WAIT_30", Type: "FLOOD_WAIT", Argument: 30}

	got := describeRPCError(err)

	if !strings.Contains(got, "30") {
		t.Fatalf("describeRPCError = %q, want the argument kept", got)
	}
}

// An RPC error nested behind our own wrapping still has to be recognised.
func TestDescribeRPCErrorUnwraps(t *testing.T) {
	inner := &tgerr.Error{Code: 400, Message: "PEER_ID_INVALID", Type: "PEER_ID_INVALID"}
	wrapped := fmt.Errorf("forwarding: %w", inner)

	if got := describeRPCError(wrapped); !strings.HasPrefix(got, "PEER_ID_INVALID") {
		t.Fatalf("describeRPCError = %q, want the unwrapped type", got)
	}
}

func TestDescribeRPCErrorPassesThroughPlainErrors(t *testing.T) {
	if got := describeRPCError(errors.New("peer chat:9 not found")); got != "peer chat:9 not found" {
		t.Fatalf("describeRPCError = %q, want the original text", got)
	}
	if got := describeRPCError(nil); got != "" {
		t.Fatalf("describeRPCError(nil) = %q, want empty", got)
	}
}
