package hub

import (
	"context"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Configuration enters through the command layer's prepare and commit callbacks.

type Registration = hostset.Registration
type Registered = hostset.Registered

var ErrIncompatible = hostset.ErrIncompatible

func RegisterCode(ctx context.Context, req Registration, code protocol.RegistrationCode, prepare, commit func(Registered) error) error {
	return hostset.RegisterCode(ctx, req, code, prepare, commit)
}

func RecoverRegistration(ctx context.Context, host Registered, id string, commit func(Registered) error) error {
	return hostset.RecoverRegistration(ctx, host, id, commit)
}
