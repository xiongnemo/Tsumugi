package telegram

import (
	"errors"
	"fmt"

	"github.com/gotd/td/tgerr"
)

// describeRPCError turns a Telegram RPC failure into something that survives the status bar.
//
// The status bar's middle column truncates, and gotd's own error text reads
// "rpc error code 400: CHAT_FORWARDS_RESTRICTED (0)" — with our own operation prefix in front of
// it, the part that actually says what went wrong is exactly what falls off the end. The error
// type is the actionable token, so it leads.
//
// Non-RPC errors are returned unchanged; they are usually short already.
func describeRPCError(err error) string {
	if err == nil {
		return ""
	}
	var rpc *tgerr.Error
	if errors.As(err, &rpc) {
		if rpc.Argument != 0 {
			return fmt.Sprintf("%s %d (%d)", rpc.Type, rpc.Argument, rpc.Code)
		}
		return fmt.Sprintf("%s (%d)", rpc.Type, rpc.Code)
	}
	return err.Error()
}

// rpcError wraps a failure for display, keeping the operation name but replacing gotd's verbose
// text with the error type.
func rpcError(operation string, err error) error {
	return fmt.Errorf("%s: %s", operation, describeRPCError(err))
}
