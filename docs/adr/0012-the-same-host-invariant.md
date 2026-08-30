# A Harness reaches only its own Host's Vendor, and no type on the wire can say otherwise

The Data Plane never crosses the network. A Harness always reaches a Vendor on the same machine over
loopback, and the Control Plane carries no prompt for a Harness-backed Session. Charting rejected
cross-Host Sessions because they drag inference traffic back onto the network and reintroduce auth,
tunnelling and a streaming proxy on the highest-volume path. This ADR records why the invariant needs
no runtime check to hold, and names the one place it is a convention rather than a fact.

## It is structural in three places

A cross-Host Session would have to be expressed somewhere. There are three candidates and none of
them can carry it.

**The Session spec.** `harness.SessionSpec.Vendor` is a `vendors.Endpoint`, and the Daemon fills it
from its own adapter list. It is never decoded from a request body, so no caller can put an address
in it. The Harness Adapter receives an endpoint and cannot ask for a different one.

**The wire.** `POST /v1/sessions` carries a Model id and a Harness name. It carries no address and no
Host id, because [ADR 0009](0009-wire-protocol-and-event-log.md) put the Host id in the path prefix
that the Hub strips before forwarding. A Model id is unique inside one Vendor on one Host and
nowhere else, so it cannot name a Model somewhere else even by accident: it names one on the Host
that received the request or it names nothing.

**The config.** `config.Daemon` lists this Host's Vendors and has no field for another Host, which is
fence one of [ADR 0010](0010-go-package-structure-and-seams.md). A config file that tries fails at
startup as an unknown field.

So the invariant is not enforced by a check that could be forgotten. It holds because the three
places a violation could be written down do not have room for one.

## The one place it is a convention

`vendors.Endpoint.Base` is a string in the Host's own config, and nothing stops a user writing
`http://192.168.1.40:11434` into it. The Daemon would use it, the Session would work, and inference
would cross the network.

**v1 does not refuse that, and logs a warning when `Base` is not a loopback address.** A loopback
literal is the obvious check and it is wrong: a Vendor in a container answers on a bridge address
like `http://172.17.0.1:11434`, which is the same machine and is not `127.0.0.1`. Refusing it breaks
a setup that honours the invariant, to catch a user who deliberately typed another machine's address
into a file that describes their own machine. The warning is the honest size of the problem.

What this means for the Session record is already settled and unchanged: a Session records the Vendor
and Model that served it, never the ones it was configured with.

## Considered options

- **Refuse a non-loopback `Base` at config load.** One line, and it turns the convention into a fact.
  Rejected for the container case above. It fails a legitimate setup to prevent a deliberate one.
- **Resolve `Base` and compare against the machine's own interfaces.** Catches the container case and
  the LAN case. Rejected: it reads the network at startup, it is wrong on a machine that gains an
  interface later, and it spends real complexity on a rule the wire cannot express anyway.
- **Say nothing.** Rejected only because the warning is free and the silence would leave the one hole
  in this ADR undocumented in the running program.

## Consequences

- The Data Plane has no network component in v1, so nothing in the design needs a streaming proxy, a
  second auth story, or a timeout budget that spans two machines.
- Admission is per Host and could not be otherwise. VRAM is a per-Host resource and a Daemon knows
  only its own Host, so a global limit would bound the wrong thing with the wrong information.
- Adding cross-Host Sessions later is not a configuration change. It needs a Host id on the start
  command, a Vendor address the Daemon accepts from a caller, and an answer for what happens when the
  Vendor's Host goes `Down` mid-Prompt. That is a redesign, and this ADR is why it stays one.
