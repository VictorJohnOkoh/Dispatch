# Dispatch

Drive Harnesses that run on other machines you own, from a browser, with the Model behind each one
chosen when the Session starts. A Harness is a program that turns a Model into an agent: it runs the
tool-calling loop, manages context, and decides what to do next.

A Daemon lives on each Host and owns everything that happens there: it spawns the Harness, holds its
stdin, answers its approval requests itself, writes every normalised Event to a durable log, and
kills the process group when you say stop. A Hub holds a connection to every Host, merges what they
report, and serves the Client. Reach is an SSH tunnel, so the Daemons bind loopback and nothing
hand-written faces the internet.

Sessions survive the laptop closing. The Event log is the transport, the replay buffer and the
history at once, so reattaching and restarting are the same mechanism.

