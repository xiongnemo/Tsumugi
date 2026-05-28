package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"github.com/nemo/Tsumugi/internal/i18n"
)

type uiAuth struct {
	phone  string
	events chan<- Event
}

func newUIAuth(phone string, events chan<- Event) auth.UserAuthenticator {
	return &uiAuth{
		phone:  phone,
		events: events,
	}
}

func (a *uiAuth) Phone(ctx context.Context) (string, error) {
	if a.phone != "" {
		return a.phone, nil
	}
	return a.prompt(ctx, AuthPrompt{
		Kind:      AuthPromptPhone,
		TitleKey:  i18n.KeyAuthTitleLogin,
		LabelKey:  i18n.KeyAuthLabelPhone,
		CanCancel: true,
	})
}

func (a *uiAuth) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	return a.prompt(ctx, AuthPrompt{
		Kind:      AuthPromptCode,
		TitleKey:  i18n.KeyAuthTitleCode,
		LabelKey:  i18n.KeyAuthLabelCode,
		HelpMsg:   sentCodeHint(sentCode),
		CanCancel: true,
	})
}

func (a *uiAuth) Password(ctx context.Context) (string, error) {
	return a.prompt(ctx, AuthPrompt{
		Kind:      AuthPromptPassword,
		TitleKey:  i18n.KeyAuthTitlePassword,
		LabelKey:  i18n.KeyAuthLabelPassword,
		Secret:    true,
		CanCancel: true,
	})
}

func (a *uiAuth) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	_ = tos
	sendEvent(ctx, a.events, Event{
		Kind:      EventStatus,
		StatusMsg: i18n.M(i18n.KeyStatusTOSAcceptance),
	})
	return nil
}

func (a *uiAuth) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign up is not implemented in Tsumugi yet; create the account in an official Telegram client first")
}

func (a *uiAuth) prompt(ctx context.Context, prompt AuthPrompt) (string, error) {
	prompt.Reply = make(chan AuthResponse, 1)
	sendEvent(ctx, a.events, Event{
		Kind:      EventAuthPrompt,
		Auth:      &prompt,
		StatusMsg: i18n.M(prompt.TitleKey),
	})

	select {
	case response := <-prompt.Reply:
		if response.Err != nil {
			return "", response.Err
		}
		return strings.TrimSpace(response.Value), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(10 * time.Minute):
		return "", fmt.Errorf("timed out waiting for %s", i18n.T(prompt.LabelKey))
	}
}

func sentCodeHint(sentCode *tg.AuthSentCode) i18n.Msg {
	if sentCode == nil || sentCode.Type == nil {
		return i18n.M(i18n.KeyAuthHintCode)
	}
	return i18n.M(i18n.KeyAuthHintCodeDelivery, sentCodeDelivery(sentCode.Type))
}

func sentCodeDelivery(codeType tg.AuthSentCodeTypeClass) string {
	switch codeType.(type) {
	case *tg.AuthSentCodeTypeApp:
		return "app"
	case *tg.AuthSentCodeTypeSMS:
		return "SMS"
	case *tg.AuthSentCodeTypeCall:
		return "call"
	case *tg.AuthSentCodeTypeFlashCall:
		return "flash call"
	case *tg.AuthSentCodeTypeMissedCall:
		return "missed call"
	case *tg.AuthSentCodeTypeFragmentSMS:
		return "Fragment SMS"
	case *tg.AuthSentCodeTypeFirebaseSMS:
		return "Firebase SMS"
	case *tg.AuthSentCodeTypeEmailCode:
		return "email"
	default:
		return fmt.Sprintf("%T", codeType)
	}
}
