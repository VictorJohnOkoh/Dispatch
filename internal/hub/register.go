package hub

import (
	"context"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Configuration enters through the command layer's prepare and commit callbacks.

type Registration = hostset.Registration
type Registered = hostset.Registered
type Login = hostset.Login

var ErrIncompatible = hostset.ErrIncompatible

func RegisterCode(ctx context.Context, req Registration, code protocol.RegistrationCode, prepare, commit func(Registered) error) error {
	return hostset.RegisterCode(ctx, req, code, prepare, commit)
}

func RegisterLogin(ctx context.Context, req Registration, in Login, commit func(Registered) error) error {
	return hostset.RegisterLogin(ctx, req, in, commit)
}

func RecoverRegistration(ctx context.Context, host Registered, id string, commit func(Registered) error) error {
	return hostset.RecoverRegistration(ctx, host, id, commit)
}
