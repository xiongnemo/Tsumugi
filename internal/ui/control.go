package ui

import "github.com/nemo/Tsumugi/internal/config"

type ControlKind string

const (
	ControlOnboardingComplete ControlKind = "onboarding_complete"
	ControlLogout             ControlKind = "logout"
)

type ControlEvent struct {
	Kind   ControlKind
	Config config.Config
	Reply  chan error
}

func (e ControlEvent) respond(err error) {
	if e.Reply == nil {
		return
	}
	select {
	case e.Reply <- err:
	default:
	}
}
