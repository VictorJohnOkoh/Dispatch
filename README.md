# Capstone
An app that allows for remote connection with host devices running local LLM models, mostly a learning experience
---
The goal is that there is a central managing system that allows the user to view the progress of threads on other machines and a Daemon would live on these remote machines handling things like delivering prompts in the right format for the selected harness or managing the memory of the system so if a new thread attempts to spin up while the system doesn't have enough memory available then its blocked or if memory is being occupied by models that aren't in use then they can be unloaded after a specific timeout and then a needed model can be loaded in
Then the system can be connected to remotely via SSH (maybe over Tailscale in the future)
