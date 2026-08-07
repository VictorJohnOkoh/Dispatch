# Host reachability and presence

Type: grilling
Status: open
Blocked by: —

## Question

The Client must show which Hosts are available and which are switched off or unreachable, so the user knows where they can start a Session before they try. Design that model.

- **What states does a Host have?** "Off", "on but Daemon down", "Daemon up but Vendor down", "up but SSH tunnel broken", "up but protocol version mismatch" are all distinguishable and all mean different things to the user. Which distinctions earn their place, and which collapse?
- **How is presence detected?** Polling on an interval, a persistent connection whose liveness *is* the signal, or a heartbeat over the existing SSE stream? The Hub already holds a connection per Host — does presence fall out of that for free, or does it need its own mechanism?
- **There is no auto-discovery available.** [Vendor discovery, capability and health APIs](./02-vendor-discovery-apis.md) confirmed that no Vendor advertises itself over mDNS or anything else. Hosts and their Vendors must be explicitly configured, and any probing is ours to build. That makes presence entirely a polling-or-connection question, not a discovery one.
- **Vendor health is not uniform, so "Vendor down" may not be knowable.** Only llama.cpp distinguishes idle from loading from busy; Ollama exposes no busy signal at all and an in-progress model load is unobservable. Decide what the Host state actually claims about its Vendors, and resist promising a distinction the underlying API cannot support.
- **Who owns the state machine?** Presumably the Hub, since Daemons are ignorant of each other. Confirm, and define the transitions and their triggers.
- **Reconnection.** Backoff strategy, and whether a Host that has been down for a long time is polled differently from one that just blinked.
- **What does the user see while a Host is transitioning?** A Host that is booting is not the same as one that is off, and thrashing the UI between states is worse than being slightly slow to update.
- **What happens to a Session's Events during a Host outage?** They keep accumulating in the Daemon's Event log. On reconnect, does the Hub replay the gap, and is that visible or silent?
- **Does an unreachable Host's last-known state stay visible**, marked stale — or does it vanish from the Client?

This ticket is unblocked by the research because it concerns the Hub-to-Daemon relationship, which owes nothing to Harness or Vendor specifics.

Use `/grilling` and `/domain-modeling`. Any term that survives goes into `CONTEXT.md`.
