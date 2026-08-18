package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/media"
)

// authenticateQR runs the QR login flow.
//
// loggedIn must have been created from the dispatcher before client.Run started; see
// registerQRLogin. Without that handler the success signal is never delivered and the flow hangs
// until the token expires, forever.
func (c *GotdClient) authenticateQR(ctx context.Context, client *telegram.Client, events chan<- Event, loggedIn qrlogin.LoggedIn) error {
	if loggedIn == nil {
		return errors.New("qr login: token handler was not registered before the client started")
	}
	flow := client.QR()
	_, err := flow.Auth(ctx, loggedIn, func(ctx context.Context, token qrlogin.Token) error {
		// Auth re-invokes this on every token expiry, so the overlay has to re-render in place.
		// Rebuilding the root each time would stack roots and reset focus.
		c.sendQRPrompt(ctx, events, token)
		return nil
	})
	if err == nil {
		return nil
	}
	// gotd's qrlogin has no password path: with a cloud password set, the post-scan import
	// returns SESSION_PASSWORD_NEEDED. Anything failing after the scan is treated as "offer the
	// password prompt", so a mismatch degrades to the phone flow rather than dead-ending.
	if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
		return c.completeQRPassword(ctx, client, events)
	}
	return fmt.Errorf("qr auth: %w", err)
}

// completeQRPassword finishes a QR login for an account with two-step verification, reusing the
// existing password prompt overlay.
func (c *GotdClient) completeQRPassword(ctx context.Context, client *telegram.Client, events chan<- Event) error {
	prompt := AuthPrompt{
		Kind:     AuthPromptPassword,
		TitleKey: i18n.KeyAuthTitlePassword,
		LabelKey: i18n.KeyAuthLabelPassword,
		Secret:   true,
		Reply:    make(chan AuthResponse, 1),
		HelpMsg:  i18n.M(i18n.KeyAuthQRPasswordHelp),
	}
	sendEvent(ctx, events, Event{Kind: EventAuthPrompt, Auth: &prompt})
	var reply AuthResponse
	select {
	case <-ctx.Done():
		return ctx.Err()
	case reply = <-prompt.Reply:
	}
	if reply.Err != nil {
		return reply.Err
	}
	if _, err := client.Auth().Password(ctx, reply.Value); err != nil {
		return fmt.Errorf("qr auth password: %w", err)
	}
	return nil
}

// sendQRPrompt renders the login token as a QR code and sends it to the UI.
//
// The raw tg://login URL always accompanies the code. It is the escape hatch for any
// terminal-specific rendering failure, and it costs one line.
func (c *GotdClient) sendQRPrompt(ctx context.Context, events chan<- Event, token qrlogin.Token) {
	url := token.URL()
	preview := ""
	if img, err := media.QRBitmap(url, media.QRQuietZone); err == nil {
		preview = media.ANSISGRToTview(media.QRTerminalANSI(img))
	}
	sendEvent(ctx, events, Event{
		Kind: EventAuthPrompt,
		Auth: &AuthPrompt{
			Kind:      AuthPromptQR,
			TitleKey:  i18n.KeyAuthQRTitle,
			LabelKey:  i18n.KeyAuthQRLabel,
			QRPreview: preview,
			QRURL:     url,
			HelpMsg:   i18n.M(i18n.KeyAuthQRHelp),
			CanCancel: true,
		},
	})
}

// registerQRLogin wires the login-token handler and returns the success signal.
//
// Must be called on the dispatcher before client.Run: Telegram delivers updateLoginToken through
// the normal update stream, and a handler added later never sees it.
func registerQRLogin(dispatcher *tg.UpdateDispatcher) qrlogin.LoggedIn {
	return qrlogin.OnLoginToken(dispatcher)
}

// qrFlowFallback builds the phone-based flow used when QR is unavailable or declined.
func (c *GotdClient) qrFlowFallback(events chan<- Event) auth.Flow {
	return auth.NewFlow(newUIAuth(c.cfg.Phone, events), auth.SendCodeOptions{})
}
