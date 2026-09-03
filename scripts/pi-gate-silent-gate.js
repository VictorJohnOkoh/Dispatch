/**
 * A Gate that loads and then fails to announce. Loaded only by
 * `pi-gate-capture.py`, to capture the second failure shape.
 *
 * An unparseable extension kills Pi outright, which is easy to see. This one
 * parses, registers its `tool_call` handler, and throws in `session_start`, so
 * Pi keeps running and the announcement never arrives. That is the shape the
 * Daemon must catch by itself.
 */

export default function (pi) {
	pi.on("session_start", async () => {
		throw new Error("the Gate failed to announce");
	});

	pi.on("tool_call", async () => undefined);
}
