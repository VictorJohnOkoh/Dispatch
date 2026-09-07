package hub

import (
	"context"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
)

// Host Registration, as ADR 0013 draws the seam. The command layer supplies the
// values the user typed, the two things only a person can answer, and the write
// that commits the configuration. Everything between them — SSH trust, the Hub's
// managed identity, the key on the Host, the order and the rollback — is here.

type Registration = hostset.Registration
type Interaction = hostset.Interaction
type Registered = hostset.Registered

// The two failures a caller tells apart: a user who did not recognise the Host,
// and a Daemon serving another protocol version.
var (
	ErrDeclined     = hostset.ErrDeclined
	ErrIncompatible = hostset.ErrIncompatible
)

func RegisterHost(ctx context.Context, req Registration, ask Interaction, commit func(Registered) error) error {
	return hostset.RegisterHost(ctx, req, ask, commit)
}
