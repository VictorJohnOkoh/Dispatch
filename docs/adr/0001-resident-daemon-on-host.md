# A resident Daemon on each Host owns Session lifecycle, not the SSH connection

Sessions could have been children of an SSH connection — spawn a Harness over SSH, stream its stdout back, and let it die when the connection drops. Instead each Host runs a long-lived Daemon that owns its own Session registry, supervises Harness processes, and serves an API; SSH is demoted to bootstrap and transport. We chose this because a Session must survive the user closing their laptop, and because reaching the system from outside the LAN should be a transport change rather than a redesign.

## Considered options

- **Sessions as children of the SSH connection.** Simplest possible, nothing deployed to the Host. Rejected: a Session dies whenever the connection does, which is most of the time in practice.
- **SSH plus a detached process** (`tmux`, `nohup`, `systemd-run`). Sessions survive, nothing to deploy. Rejected: reattaching to a detached process's output stream is unreliable and would have been fought indefinitely.
- **A resident Daemon.** Chosen.

## Consequences

- There are two deployables rather than one, and a Client/Daemon version skew failure mode that must be handled explicitly by a protocol version handshake on connect.
- Session state becomes derivable from a durable Event log rather than from the liveness of a connection, which is what makes reconnect, replay and restart-survival the same mechanism.
- It is the Daemon, not a shell, that enforces containment — so Workspace Root validation and Approval Policy have somewhere to live.
- The engineering this project exists to teach — process supervision, a session registry, stream multiplexing, admission control — only exists because of this choice. Under the SSH options the project is shell scripting with a UI.
