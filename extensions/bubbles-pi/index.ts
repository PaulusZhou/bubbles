/**
 * Bubbles Pi Extension
 *
 * 把 pi 会话接入 bubblesd：
 *  - feishu_notify:   向飞书会话/群发送通知（经 bubblesd IPC `feishu.notify`）
 *  - bubbles_task:    在会话内创建 / 查询 / 立即运行定时任务（`task.*`）
 *  - bubbles_status:  查询 bubblesd 运行状态（`daemon.status`）
 *  - bubbles_holding: 查看 / 新增 / 修改 / 删除持仓（`holding.*`，SQLite 持仓库）
 *
 * 安装：把本目录复制到 ~/.pi/agent/extensions/bubbles-pi/ 或项目 .pi/extensions/。
 * 依赖 bubblesd 正在运行（unix socket，默认 ~/.bubbles/bubblesd.sock，
 * 可用 BUBBLES_DATA_DIR 环境变量覆盖数据目录）。
 */

import * as net from "node:net";
import * as os from "node:os";
import * as path from "node:path";
import { Type } from "@earendil-works/pi-ai";
import { defineTool, type ExtensionAPI } from "@earendil-works/pi-coding-agent";

// ---------------------------------------------------------------------------
// bubblesd IPC client: one JSON request per unix-socket connection.
// ---------------------------------------------------------------------------

function socketPath(): string {
	const dataDir = process.env.BUBBLES_DATA_DIR || path.join(os.homedir(), ".bubbles");
	return path.join(dataDir, "bubblesd.sock");
}

interface IpcResponse {
	result?: unknown;
	error?: string;
}

function callBubblesd(method: string, params: unknown, timeoutMs = 15000): Promise<IpcResponse> {
	return new Promise((resolve, reject) => {
		const sock = net.connect(socketPath());
		let buf = "";
		let settled = false;

		const finish = (err?: Error, resp?: IpcResponse) => {
			if (settled) return;
			settled = true;
			sock.destroy();
			if (err) reject(err);
			else resolve(resp as IpcResponse);
		};

		const timer = setTimeout(() => finish(new Error(`bubblesd ${method} timed out`)), timeoutMs);
		timer.unref?.();

		sock.on("connect", () => {
			sock.write(`${JSON.stringify({ method, params })}\n`);
		});
		sock.on("data", (chunk) => {
			buf += chunk.toString("utf8");
			try {
				const resp = JSON.parse(buf) as IpcResponse;
				clearTimeout(timer);
				finish(undefined, resp);
			} catch {
				// 响应未接收完整，继续等待
			}
		});
		sock.on("error", (err) => {
			clearTimeout(timer);
			finish(new Error(`bubblesd IPC error (${method}): ${err.message}`));
		});
		sock.on("close", () => {
			clearTimeout(timer);
			finish(new Error(`bubblesd closed the connection before responding (${method})`));
		});
	});
}

async function callBubblesdResult(method: string, params: unknown): Promise<unknown> {
	const resp = await callBubblesd(method, params);
	if (resp.error) {
		throw new Error(resp.error);
	}
	return resp.result;
}

// ---------------------------------------------------------------------------
// Tools
// ---------------------------------------------------------------------------

const feishuNotifyTool = defineTool({
	name: "feishu_notify",
	label: "Feishu Notify",
	description:
		"Send a text notification to a Feishu chat through bubblesd. " +
		"Use it to proactively report progress or results to the user. " +
		"chat_id is optional and falls back to the configured default chat.",
	annotations: { readOnlyHint: false, destructiveHint: false, openWorldHint: true },
	parameters: Type.Object({
		text: Type.String({ description: "Notification text (supports Feishu markdown in cards; plain text here)" }),
		chat_id: Type.Optional(Type.String({ description: "Target Feishu chat ID; defaults to the configured chat" })),
	}),

	async execute(_toolCallId, params, _signal, _onUpdate, _ctx) {
		const result = await callBubblesdResult("feishu.notify", {
			chat_id: params.chat_id ?? "",
			text: params.text,
		});
		return {
			content: [{ type: "text", text: "Notification delivered to Feishu." }],
			details: result,
		};
	},
});

const bubblesTaskTool = defineTool({
	name: "bubbles_task",
	label: "Bubbles Task",
	description:
		"Manage scheduled agent tasks in bubbles: create cron/one-time tasks, " +
		"list existing tasks, or run a task immediately. A created task executes " +
		"the given prompt with the configured agent on its schedule.",
	annotations: { readOnlyHint: false, destructiveHint: true, openWorldHint: true },
	parameters: Type.Object({
		action: Type.Union([Type.Literal("create"), Type.Literal("list"), Type.Literal("run")], {
			description: "What to do",
		}),
		name: Type.Optional(Type.String({ description: "Task name (required for create)" })),
		prompt: Type.Optional(Type.String({ description: "Prompt for the agent (required for create)" })),
		schedule: Type.Optional(Type.String({ description: "5-field cron expression (e.g. \"0 9 * * *\")" })),
		run_at: Type.Optional(Type.String({ description: "One-time run time in RFC3339 (e.g. 2026-01-01T09:00:00+08:00)" })),
		task_id: Type.Optional(Type.String({ description: "Task ID (required for run)" })),
	}),

	async execute(_toolCallId, params, _signal, _onUpdate, _ctx) {
		switch (params.action) {
			case "create": {
				if (!params.name || !params.prompt) {
					throw new Error("create requires name and prompt");
				}
				if (!params.schedule && !params.run_at) {
					throw new Error("create requires schedule (cron) or run_at (RFC3339)");
				}
				const result = await callBubblesdResult("task.create", {
					name: params.name,
					prompt: params.prompt,
					schedule: params.schedule,
					run_at: params.run_at,
				});
				return {
					content: [{ type: "text", text: `Task created: ${JSON.stringify(result)}` }],
					details: result,
				};
			}
			case "list": {
				const result = await callBubblesdResult("task.list", {});
				return {
					content: [{ type: "text", text: JSON.stringify(result, null, 2) }],
					details: result,
				};
			}
			case "run": {
				if (!params.task_id) {
					throw new Error("run requires task_id");
				}
				const result = await callBubblesdResult("task.run", { id: params.task_id });
				return {
					content: [{ type: "text", text: `Task run requested: ${JSON.stringify(result)}` }],
					details: result,
				};
			}
		}
	},
});

const bubblesStatusTool = defineTool({
	name: "bubbles_status",
	label: "Bubbles Status",
	description: "Report the bubblesd daemon status: uptime, task counts, and running state.",
	annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
	parameters: Type.Object({}),

	async execute(_toolCallId, _params, _signal, _onUpdate, _ctx) {
		const result = await callBubblesdResult("daemon.status", {});
		return {
			content: [{ type: "text", text: JSON.stringify(result, null, 2) }],
			details: result,
		};
	},
});

const bubblesHoldingTool = defineTool({
	name: "bubbles_holding",
	label: "Bubbles Holding",
	description:
		"View and manage portfolio holdings stored in the bubblesd SQLite database. " +
		"Use action list/get to read holdings (with cost, stop_loss, target, hypothesis, " +
		"state, review_date), and upsert/delete to modify them. Upsert is a partial " +
		"update: omitted fields keep their stored values; name is required when creating. " +
		"state is one of: holding, watchlist, validating, pending_decision, falsified, sold.",
	annotations: { readOnlyHint: false, destructiveHint: false, openWorldHint: false },
	parameters: Type.Object({
		action: Type.Union(
			[
				Type.Literal("list"),
				Type.Literal("get"),
				Type.Literal("upsert"),
				Type.Literal("delete"),
			],
			{ description: "What to do" },
		),
		ticker: Type.Optional(Type.String({ description: "Ticker like HK.01810 / 603662.SH (required for get/upsert/delete)" })),
		name: Type.Optional(Type.String({ description: "Display name, required when creating a holding" })),
		market: Type.Optional(Type.String({ description: "Market: 港股 / A股 / 美股" })),
		qty: Type.Optional(Type.Number({ description: "Position size in shares" })),
		cost: Type.Optional(Type.Number({ description: "Average cost price" })),
		state: Type.Optional(Type.String({ description: "holding | watchlist | validating | pending_decision | falsified | sold" })),
		hypothesis: Type.Optional(Type.String({ description: "Falsifiable investment hypothesis" })),
		stop_loss: Type.Optional(Type.Number({ description: "Stop-loss price" })),
		target: Type.Optional(Type.Number({ description: "Target price" })),
		review_date: Type.Optional(Type.String({ description: "Mandatory review date YYYY-MM-DD" })),
		decision: Type.Optional(Type.String({ description: "Decision log ID, e.g. D-20261018-01" })),
		notes: Type.Optional(Type.String({ description: "Free-form notes" })),
	}),

	async execute(_toolCallId, params, _signal, _onUpdate, _ctx) {
		switch (params.action) {
			case "list": {
				const result = await callBubblesdResult("holding.list", {});
				return {
					content: [{ type: "text", text: JSON.stringify(result, null, 2) }],
					details: result,
				};
			}
			case "get": {
				if (!params.ticker) throw new Error("get requires ticker");
				const result = await callBubblesdResult("holding.get", { ticker: params.ticker });
				return {
					content: [{ type: "text", text: JSON.stringify(result, null, 2) }],
					details: result,
				};
			}
			case "upsert": {
				if (!params.ticker) throw new Error("upsert requires ticker");
				const fields: Record<string, unknown> = { ticker: params.ticker };
				for (const key of [
					"name",
					"market",
					"qty",
					"cost",
					"state",
					"hypothesis",
					"stop_loss",
					"target",
					"review_date",
					"decision",
					"notes",
				]) {
					const v = (params as Record<string, unknown>)[key];
					if (v !== undefined) fields[key] = v;
				}
				const result = await callBubblesdResult("holding.upsert", fields);
				return {
					content: [
						{
							type: "text",
							text: `Holding saved: ${JSON.stringify(result)}`,
						},
					],
					details: result,
				};
			}
			case "delete": {
				if (!params.ticker) throw new Error("delete requires ticker");
				const result = await callBubblesdResult("holding.delete", { ticker: params.ticker });
				return {
					content: [{ type: "text", text: `Holding deleted: ${params.ticker}` }],
					details: result,
				};
			}
		}
	},
});

// ---------------------------------------------------------------------------
// 审批门
// ---------------------------------------------------------------------------

const dangerousBashPatterns = [
	/\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|--recursive)/i,
	/\bsudo\b/i,
	/\b(mkfs|dd)\b/i,
	/\b(shutdown|reboot|halt)\b/i,
	/\bgit\s+push\b.*--force/i,
	/\b(chmod|chown)\b.*\s777\b/i,
	/\bmkswap\b|\/dev\/sd/i,
];

function toolNeedsApproval(pi: ExtensionAPI, toolName: string, input: unknown): boolean {
	const tool = pi.getAllTools().find((t) => t.name === toolName);
	// 显式声明为破坏性的工具（如 bubbles_task）一律确认。
	if (tool?.annotations?.destructiveHint === true) {
		return true;
	}
	// 持仓写操作（upsert/delete）需确认；只读的 list/get 放行。
	if (toolName === "bubbles_holding") {
		const action = (input as { action?: string })?.action ?? "";
		return action === "upsert" || action === "delete";
	}
	// 已知危险的 shell 命令需要确认。
	if (/bash/i.test(toolName)) {
		const command = (input as { command?: string })?.command ?? "";
		return dangerousBashPatterns.some((p) => p.test(command));
	}
	return false;
}

function summarizeInput(input: unknown): string {
	const text = JSON.stringify(input);
	if (text === undefined) return "";
	return text.length > 400 ? `${text.slice(0, 400)}…` : text;
}

// ---------------------------------------------------------------------------
// Extension entry
// ---------------------------------------------------------------------------

export default function (pi: ExtensionAPI) {
	pi.registerTool(feishuNotifyTool);
	pi.registerTool(bubblesTaskTool);
	pi.registerTool(bubblesStatusTool);
	pi.registerTool(bubblesHoldingTool);

	// 审批门：危险工具调用需人工确认（飞书卡片按钮 / 终端对话框）。
	// 仅在有 UI 时启用（交互终端或 bubblesd 的 RPC 通道）；定时任务等
	// 无 UI 场景保持原有自动执行行为。
	pi.on("tool_call", async (event, ctx) => {
		if (!ctx.hasUI || !toolNeedsApproval(pi, event.toolName, event.input)) {
			return undefined;
		}
		const ok = await ctx.ui.confirm(
			"🔐 工具调用确认",
			`是否允许执行 **${event.toolName}**？\n\n\`${summarizeInput(event.input)}\``,
		);
		if (!ok) {
			return { block: true, reason: "用户拒绝了该工具调用" };
		}
		return undefined;
	});

	// /bubbles — 查看 daemon 状态的便捷命令
	pi.registerCommand("bubbles", {
		description: "Show bubblesd status",
		handler: async (_args, ctx) => {
			try {
				const result = await callBubblesdResult("daemon.status", {});
				ctx.ui.notify(`bubblesd: ${JSON.stringify(result)}`, "info");
			} catch (err) {
				ctx.ui.notify(`bubblesd unreachable: ${(err as Error).message}`, "error");
			}
		},
	});
}
