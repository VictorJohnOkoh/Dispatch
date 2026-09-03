/**
 * The Dispatch approval Gate for Pi.
 *
 * Pi has no built-in tool approval, so the Daemon ships this one. It differs
 * from Pi's bundled `permission-gate.ts` in the two ways ADR 0008 needs: it
 * classifies and gates every tool call rather than three bash regexes, and it
 * announces itself on `session_start` so the Daemon can tell a loaded Gate from
 * an extension that never loaded.
 *
 * Both frames carry JSON inside a display field, because Pi's extension UI
 * protocol has no structured payload: `notify` has `message`, `select` has
 * `title`. That is also what carries the `toolCallId` the UI request otherwise
 * drops.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const PROTOCOL = "dispatch.gate/1";
const KINDS = ["read", "edit", "execute", "fetch", "other"];

// Pi's eight built-in tools, plus the names a fetch tool would plausibly take.
// Pi ships no fetch tool, and listing the names keeps that slot reachable if an
// extension registers one instead of folding it into "other".
const TOOL_KINDS: Record<string, string> = {
	read: "read",
	grep: "read",
	find: "read",
	ls: "read",
	edit: "edit",
	write: "edit",
	bash: "execute",
	powershell: "execute",
	fetch: "fetch",
	webfetch: "fetch",
	web_fetch: "fetch",
};

export default function (pi: ExtensionAPI) {
	pi.on("session_start", async (event, ctx) => {
		ctx.ui.notify(
			JSON.stringify({ protocol: PROTOCOL, event: "ready", reason: event.reason, kinds: KINDS }),
			"info",
		);
	});

	pi.on("tool_call", async (event, ctx) => {
		const request = JSON.stringify({
			protocol: PROTOCOL,
			event: "request",
			toolCallId: event.toolCallId,
			toolName: event.toolName,
			kind: TOOL_KINDS[event.toolName] ?? "other",
		});

		const answer = await ctx.ui.select(request, ["allow", "deny"]);
		if (answer === "allow") return undefined;

		// undefined is a cancelled or timed-out dialog. A Gate that fails open is
		// not a Gate.
		const reason = answer === "deny" ? "denied by Dispatch" : "no answer from Dispatch";
		return { block: true, reason };
	});
}
