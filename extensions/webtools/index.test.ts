import assert from "node:assert/strict";
import { readFile, rm } from "node:fs/promises";
import { dirname } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import webtoolsExtension from "./index.ts";

type ExecResult = { stdout: string; stderr: string; code: number; killed: boolean };
type ExecStub = (command: string, args: string[]) => Promise<ExecResult>;

function loadTools(exec: ExecStub) {
	const tools: any[] = [];
	webtoolsExtension({
		exec,
		registerTool(tool: unknown) {
			tools.push(tool);
		},
	} as unknown as ExtensionAPI);
	return {
		search: tools.find((tool) => tool.name === "web_search"),
		fetch: tools.find((tool) => tool.name === "web_fetch"),
	};
}

const theme = {
	fg(color: string, text: string) {
		return `[${color}]${text}`;
	},
	bold(text: string) {
		return text;
	},
};

function renderedText(component: { render(width: number): string[] }) {
	return component.render(200).join("\n");
}

export default async function (_pi: ExtensionAPI) {
	const success = loadTools(async () => ({
		stdout: '[{"title":"Example","url":"https://example.com"}]\n',
		stderr: "",
		code: 0,
		killed: false,
	}));
	const successResult = await success.search.execute("test", { query: "example" }, undefined);
	assert.equal(successResult.details.resultCount, 1);
	assert.match(successResult.content[0].text, /Example/);

	const calls: Array<{ command: string; args: string[] }> = [];
	const forwarding = loadTools(async (command, args) => {
		calls.push({ command, args });
		return { stdout: "{}\n", stderr: "", code: 0, killed: false };
	});
	await forwarding.search.execute("test", { query: "default search" }, undefined);
	await forwarding.search.execute("test", { query: "limited search", limit: 7 }, undefined);
	await forwarding.fetch.execute("test", { url: "https://example.com/default" }, undefined);
	await forwarding.fetch.execute("test", {
		url: "https://example.com/limited",
		max_chars: 1234,
		max_response_bytes: 5678,
		// Unknown arguments must not provide a path to the CLI's SSRF protection override.
		allow_private: true,
	}, undefined);
	assert.deepEqual(calls, [
		{ command: "webtools", args: ["search", "default search"] },
		{ command: "webtools", args: ["search", "--limit", "7", "limited search"] },
		{ command: "webtools", args: ["fetch", "https://example.com/default"] },
		{
			command: "webtools",
			args: ["fetch", "--max-chars", "1234", "--max-response-bytes", "5678", "https://example.com/limited"],
		},
	]);
	assert.equal(forwarding.fetch.parameters.properties.allow_private, undefined);

	const failed = loadTools(async () => ({ stdout: "", stderr: "network failed", code: 1, killed: false }));
	await assert.rejects(
		() => failed.search.execute("test", { query: "example" }, undefined),
		/webtools search failed: network failed/,
	);

	const cancelled = loadTools(async () => ({ stdout: "partial", stderr: "", code: 0, killed: true }));
	const controller = new AbortController();
	controller.abort();
	await assert.rejects(
		() => cancelled.search.execute("test", { query: "example" }, controller.signal),
		/webtools search was cancelled/,
	);

	const timedOut = loadTools(async () => ({ stdout: "partial", stderr: "", code: 0, killed: true }));
	await assert.rejects(
		() => timedOut.fetch.execute("test", { url: "https://example.com" }, undefined),
		/webtools fetch timed out after 35 seconds/,
	);

	const fullOutput = "x".repeat(60_000);
	const truncated = loadTools(async () => ({ stdout: fullOutput, stderr: "", code: 0, killed: false }));
	const truncatedResult = await truncated.fetch.execute("test", { url: "https://example.com" }, undefined);
	assert.equal(truncatedResult.details.truncation.truncated, true);
	assert.match(truncatedResult.content[0].text, /Output truncated/);
	assert.equal(await readFile(truncatedResult.details.fullOutputPath, "utf8"), fullOutput);
	await rm(dirname(truncatedResult.details.fullOutputPath), { recursive: true, force: true });

	assert.equal(success.fetch.parameters.properties.max_chars.maximum, 50_000);
	assert.equal(success.fetch.parameters.properties.max_response_bytes.maximum, 5 * 1024 * 1024);

	const errorResult = { content: [{ type: "text", text: "request failed" }], details: undefined };
	const options = { expanded: false, isPartial: false };
	const context = { isError: true };
	const searchError = success.search.renderResult(errorResult, options, theme, context);
	const fetchError = success.fetch.renderResult(errorResult, options, theme, context);
	assert.match(renderedText(searchError), /\[error\]request failed/);
	assert.match(renderedText(fetchError), /\[error\]request failed/);
	assert.doesNotMatch(renderedText(searchError), /\[success\]/);
	assert.doesNotMatch(renderedText(fetchError), /\[success\]/);
}
