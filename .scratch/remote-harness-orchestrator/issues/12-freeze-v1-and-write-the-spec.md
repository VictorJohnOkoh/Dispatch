# Freeze v1 and write the build spec

Type: grilling
Status: open
Blocked by: 04, 10, 11

## Question

The destination. Everything decided across this map gets assembled into one document that someone — you, or an agent — can build from without redesigning anything.

- **Freeze the v1 feature list.** Explicitly in, explicitly out. Every "design the seam, ship the simple thing" decision gets its simple thing named precisely: one Session at a time, manual Daemon install, SSH tunnel reach, one Vendor, how many Harnesses.
- **How many Harnesses in v1?** The passthrough plus one real one is the minimum that proves the abstraction. Two real ones proves it properly and costs more. Decide with the research findings in hand rather than now.
- **Assemble the spec.** Domain model (pointing at `CONTEXT.md`, not duplicating it), component responsibilities, the interfaces from tickets 06–08, the protocol and storage design from ticket 09, the package tree from ticket 11, and the Client shape from ticket 10.
- **Build order.** What is built first such that something runs end to end early? A vertical slice — one Host, passthrough Harness, one Event kind, no approval — is likely right, but decide it deliberately.
- **What proves v1 is done?** Not a test suite; a set of observable behaviours. "Start a Session on a sleeping Host and get a useful error", "close the laptop mid-Session and reattach with full history", "approve a tool call from a phone over a tunnel."
- **Re-read every Note on the map** and confirm each still holds. Some were decided before the research landed; anything the research contradicted should be corrected here rather than silently carried into the build.
- **Write the outstanding ADRs** — the Hub/Daemon role split and the same-Host invariant, both flagged in the map's **Not yet specified** section.

When this resolves, the map is complete and implementation begins as a separate effort.

Use `/grilling` to attack the frozen scope before committing to it. The failure mode here is a v1 that is too large to finish.
