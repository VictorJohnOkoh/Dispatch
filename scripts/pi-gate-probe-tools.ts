/**
 * Two no-op tools, loaded only by `pi-gate-capture.py`.
 *
 * Pi's eight built-in tools reach three of the five ToolKinds. Nothing built in
 * is a fetch, and nothing built in falls outside the table, so without these the
 * capture could not drive the `fetch` and `other` slots at all. Neither tool
 * does anything: the point is that the Gate holds the call, not what the call
 * would have done.
 */

import { Type } from "@earendil-works/pi-ai";
import { defineTool, type ExtensionAPI } from "@earendil-works/pi-coding-agent";

const fetchTool = defineTool({
	name: "fetch",
	label: "Fetch",
	description: "Fetch the contents of a URL.",
	parameters: Type.Object({
		url: Type.String({ description: "URL to fetch" }),
	}),
	async execute(_toolCallId, params) {
		return { content: [{ type: "text", text: `fetched ${params.url}` }], details: undefined };
	},
});

const noteTool = defineTool({
	name: "note",
	label: "Note",
	description: "Record a short note.",
	parameters: Type.Object({
		text: Type.String({ description: "Text to record" }),
	}),
	async execute(_toolCallId, params) {
		return { content: [{ type: "text", text: `noted ${params.text}` }], details: undefined };
	},
});

export default function (pi: ExtensionAPI) {
	pi.registerTool(fetchTool);
	pi.registerTool(noteTool);
}
