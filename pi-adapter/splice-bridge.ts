/**
 * Splice bridge extension for Pi.
 *
 * This is the real external harness consumer for the Splice pipeline. It
 * spawns the splice-pi-bridge binary, reads its stream-json events, and
 * renders the same stage truth the Splice TUI shows. It sends typed control
 * commands on the bridge's stdin.
 *
 * Capability honesty:
 * - The only routed control is cancel_run. The bridge declares no approval,
 *   model, pause, or resume capabilities, so this extension offers none.
 * - Stage progress comes only from stage events. This extension never
 *   estimates overall progress from the stage roster.
 * - The bridge ships one deterministic fixture provider. Live model provider
 *   resolution is not wired, and the bridge fails loudly without -fixture.
 *
 * Setup:
 *   go build -o splice-pi-bridge ./cmd/splice-pi-bridge
 *   pi -e ./pi-adapter/splice-bridge.ts
 *
 * Use:
 *   /splice fix the spelling typo   Run one Splice pipeline request.
 *   /splice-cancel                  Cancel the active run.
 */

import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { existsSync } from "node:fs";
import { join, resolve } from "node:path";
import type { ExtensionAPI, ExtensionCommandContext } from "@earendil-works/pi-coding-agent";

/** One wire event from the bridge's stream-json output. */
interface BridgeEvent {
	type?: string;
	runId?: string;
	stages?: string[];
	name?: string;
	status?: string;
	progress?: number;
	reason?: string;
	text?: string;
	delta?: string;
	message?: string;
	exitCode?: number;
}

interface StageView {
	status: string;
	progress: number;
}

const CANCEL_GRACE_MS = 5000;

export default function spliceBridgeExtension(pi: ExtensionAPI): void {
	/** The active bridge child process, if a run is in flight. */
	let child: ReturnType<typeof spawn> | undefined;
	/** Stage views keyed by stage name, in roster order. */
	let roster: string[] = [];
	let stageViews = new Map<string, StageView>();
	/** True once a run_end event settled the current run. */
	let settled = true;

	function resolveBridgeBin(cwd: string): string | undefined {
		const override = process.env.SPLICE_PI_BRIDGE_BIN;
		if (override && existsSync(override)) return override;
		// Walk up from the working directory so a pi session started in any
		// subdirectory of the repository still finds the built binary.
		let dir = cwd;
		for (let depth = 0; depth < 16; depth++) {
			const candidate = join(dir, "splice-pi-bridge");
			if (existsSync(candidate)) return candidate;
			const parent = join(dir, "..");
			const resolved = resolve(parent);
			if (resolved === dir) break;
			dir = resolved;
		}
		return undefined;
	}

	function renderWidget(ctx: ExtensionCommandContext): void {
		if (!ctx.hasUI) return;
		const lines: string[] = ["Splice pipeline"];
		for (const name of roster) {
			const view = stageViews.get(name);
			const status = view?.status ?? "pending";
			switch (status) {
				case "completed":
					lines.push(`  [x] ${name}`);
					break;
				case "failed":
					lines.push(`  [!] ${name} failed`);
					break;
				case "skipped":
					lines.push(`  [-] ${name} skipped`);
					break;
				case "running": {
					const progress = typeof view?.progress === "number" ? ` ${view.progress}%` : "";
					lines.push(`  [>] ${name}${progress}`);
					break;
				}
				default:
					lines.push(`  [ ] ${name}`);
			}
		}
		ctx.ui.setWidget("splice", lines);
	}

	function setStatus(ctx: ExtensionCommandContext, text: string | undefined): void {
		if (!ctx.hasUI) return;
		ctx.ui.setStatus("splice", text);
	}

	function notify(ctx: ExtensionCommandContext, text: string, level: "info" | "warning" | "error"): void {
		if (!ctx.hasUI) return;
		ctx.ui.notify(text, level);
	}

	function applyEvent(ctx: ExtensionCommandContext, event: BridgeEvent): void {
		switch (event.type) {
			case "pipeline_plan": {
				roster = Array.isArray(event.stages) ? [...event.stages] : [];
				stageViews = new Map();
				renderWidget(ctx);
				setStatus(ctx, "Splice: planning");
				break;
			}
			case "stage": {
				if (!event.name) break;
				stageViews.set(event.name, {
					status: event.status ?? "pending",
					progress: typeof event.progress === "number" ? event.progress : 0,
				});
				renderWidget(ctx);
				if (event.status === "running") {
					const progress = typeof event.progress === "number" ? ` ${event.progress}%` : "";
					setStatus(ctx, `Splice: ${event.name}${progress}`);
				}
				break;
			}
			case "run_end": {
				settled = true;
				setStatus(ctx, undefined);
				const code = typeof event.exitCode === "number" ? event.exitCode : -1;
				if (event.status === "success") {
					notify(ctx, `Splice run completed (exit ${code})`, "info");
				} else if (event.status === "interrupted") {
					notify(ctx, `Splice run canceled (exit ${code})`, "warning");
				} else {
					notify(ctx, `Splice run ended with ${event.status ?? "unknown"} (exit ${code})`, "error");
				}
				break;
			}
			case "warning":
				if (event.message) notify(ctx, `Splice bridge: ${event.message}`, "warning");
				break;
			default:
				// text, reasoning, tool_call, usage, and unknown future event
				// types carry presentation detail this slice does not render.
				break;
		}
	}

	async function runPipeline(prompt: string, ctx: ExtensionCommandContext): Promise<void> {
		if (child && !settled) {
			notify(ctx, "A Splice run is already active. Use /splice-cancel first.", "error");
			return;
		}
		const bin = resolveBridgeBin(ctx.cwd);
		if (!bin) {
			notify(
				ctx,
				"splice-pi-bridge not found. Run: go build -o splice-pi-bridge ./cmd/splice-pi-bridge",
				"error",
			);
			return;
		}

		settled = false;
		roster = [];
		stageViews = new Map();
		renderWidget(ctx);

		const started = spawn(bin, ["-fixture", "-prompt", prompt, "-cwd", ctx.cwd], {
			cwd: ctx.cwd,
			stdio: ["pipe", "pipe", "pipe"],
		});
		child = started;

		const sawRunEnd = new Promise<void>((resolve) => {
			started.once("exit", () => resolve());
		});

		const lines = createInterface({ input: started.stdout });
		lines.on("line", (line) => {
			const trimmed = line.trim();
			if (!trimmed) return;
			let event: BridgeEvent;
			try {
				event = JSON.parse(trimmed) as BridgeEvent;
			} catch {
				return; // one bad line must not kill the reader
			}
			applyEvent(ctx, event);
		});

		started.stderr.on("data", (chunk: Buffer) => {
			const text = chunk.toString().trim();
			if (text) notify(ctx, `Splice bridge: ${text}`, "error");
		});

		await sawRunEnd;
		lines.close();
		if (child === started) child = undefined;

		if (!settled) {
			// The child exited without a run_end event. Report the crash
			// instead of leaving the widget in a running state.
			settled = true;
			setStatus(ctx, undefined);
			notify(ctx, `Splice bridge exited without a terminal event (code ${started.exitCode})`, "error");
		}
	}

	function cancelRun(ctx: ExtensionCommandContext): void {
		if (!child || settled) {
			notify(ctx, "No active Splice run.", "info");
			return;
		}
		const active = child;
		try {
			active.stdin?.write(`${JSON.stringify({ kind: "cancel_run" })}\n`);
		} catch {
			// stdin already gone; fall through to the hard kill below.
		}
		const timer = setTimeout(() => {
			try {
				active.kill("SIGKILL");
			} catch {
				// already gone
			}
		}, CANCEL_GRACE_MS);
		timer.unref();
	}

	pi.registerCommand("splice", {
		description: "Run one request through the Splice pipeline bridge",
		handler: async (args, ctx) => {
			const prompt = (args ?? "").trim();
			if (!prompt) {
				notify(ctx, "Usage: /splice <request>", "info");
				return;
			}
			await runPipeline(prompt, ctx);
		},
	});

	pi.registerCommand("splice-cancel", {
		description: "Cancel the active Splice pipeline run",
		handler: async (_args, ctx) => {
			cancelRun(ctx);
		},
	});

	pi.on("session_shutdown", async () => {
		// Cleanup: a session teardown must not leak the bridge child.
		if (!child) return;
		const active = child;
		child = undefined;
		try {
			active.kill("SIGTERM");
		} catch {
			// already gone
		}
	});
}
