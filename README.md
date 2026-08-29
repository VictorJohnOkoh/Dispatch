# Dispatch

Run AI coding agents on the machines you already own, and watch all of them from one browser tab.

## The goal

I have machines with GPUs that sit idle, and a laptop that cannot run a 20B model. This app puts
the two together. You pick a machine, a Harness to drive the agent, and a Model to back it. The app
starts the run there and streams the result to you.

The models stay local. No prompt leaves the machine it runs on. This is a learning project first,
so the interesting part is the architecture, not the feature list.

## The parts

```
  browser          laptop                machine with the GPU
  Client  --HTTP-->  Hub  --SSH-->  Daemon --> Harness --localhost--> Vendor
                                      |
                                      +-- Events travel back up the same line
```

- A **Host** is a machine you own. Each one runs a **Daemon** that starts agents and reports what
  they do.
- A **Vendor** is the program that serves the models on that Host. Ollama, LM Studio and
  llama-swap are Vendors.
- A **Harness** is the program that turns a model into an agent. It calls tools, keeps context and
  decides the next step.
- The **Hub** holds one connection to every Daemon and merges what they say. It is the only part
  that knows more than one Host exists.
- The **Client** is the browser UI. It talks to the Hub only.

`CONTEXT.md` holds the full vocabulary.

## What happens

You add a Host. The Hub opens an SSH connection to it, then opens a channel straight to the Daemon
on that machine. Nothing is published on a local port. Today the connection needs SSH to reach the
Host, so a VPN like Tailscale is enough to use this from outside the house later.

The Daemon reports which Vendors it can reach and which Models they hold. You choose a Harness, a
Model, and a directory, and you start a Session.

The Daemon checks the request before anything runs. It refuses a directory outside the Workspace
Root. It refuses a Model that does not fit in the memory that is free, and it can unload a Model
that no Session is using to make room. This is the point where a bad run is stopped, not after it
starts.

Then the Harness starts. It reaches the Vendor over localhost, so the prompt and the answer never
cross the network. Everything the Harness does becomes an **Event**: a message, a tool call, a tool
result, an error, the end of the run. The Client draws Events and never raw Harness output, which
is why two different Harnesses look the same on screen.

When the Harness wants to use a tool, the Approval Policy of that Session decides. Run it now, ask
you first, or refuse it.

Then you close the laptop. The Daemon keeps going, because the Session belongs to the Daemon and
not to your connection. When you come back, the Event log replays and the Session is where you left
it.

## When a machine goes away

The Hub never asks a Host if it is alive. It holds one Event stream per Host, and the state of that
stream is the state of the Host. A stream that drops moves the Host to `Connecting` and the Client
keeps showing the last thing it knew. Repeated failures move it to `Down`. Reconnect, replay and
restart all use the same mechanism, which is the Event log.


