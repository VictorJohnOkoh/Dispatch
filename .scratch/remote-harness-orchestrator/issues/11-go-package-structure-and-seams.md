# Go package structure and seams

Type: grilling
Status: open
Blocked by: 06, 07, 08, 09

## Question

The architecture-skill centrepiece. By this point the interfaces exist individually; this ticket decides how they compose into one Go module producing one binary with two roles.

- **The package tree.** Actual paths and actual responsibilities. What lives in `internal/`, what is exported, and why. `cmd/` layout for the `daemon` and `hub` roles.
- **What is shared between the roles, and what must not be?** Event types and protocol types are obviously shared. Is anything else? A Daemon must never gain the ability to learn about peer Hosts — does the package structure make that violation *impossible*, or merely impolite?
- **Which modules are deep?** For each package: how much complexity does it hide, and how wide is its interface? Name the shallow ones and either deepen them or justify them. Pass-through layers that exist only to forward calls are the specific failure mode to hunt.
- **Dependency direction.** Draw it and confirm it is acyclic. Which packages may import which? Where do the interfaces live — with the consumer or the implementer? In Go the answer is usually the consumer, and it changes the tree.
- **Where does concurrency live?** Which packages own goroutines, which own channels, and which are purely synchronous and therefore trivially testable. A package where anything might be concurrent is a package nobody can reason about.
- **What is testable without a Host, a Vendor, a Harness, or a GPU?** Ideally almost everything. Where the answer is "not this", say whether the seam is wrong or the thing is genuinely I/O.
- **Where does configuration enter?** Config parsing at the edge with plain values passed inward, or config structs reaching deep into the tree. The second is easier now and poisons testability later.
- **The `graduating fog` check:** this ticket's answer is what makes the testing strategy, config format and observability entries in the map's **Not yet specified** section specifiable. Flag them explicitly when it resolves.

Use `/codebase-design` — this is what that skill is for — alongside `/grilling`.
