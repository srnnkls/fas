const LOADED = Symbol.for("fas.hooks.loaded");
const TIMEOUT_MS = 20_000;

export function createFasCaller(options = {}) {
  const env = options.env ?? process.env;
  const command = env.FAS_BIN ?? "fas";
  const args = ["eval", "--harness", "pi", ...(options.args ?? [])];
  let loading;
  const spawned = () =>
    options.spawn
      ? Promise.resolve(options.spawn)
      : (loading ??= import("node:child_process").then((module) => module.spawn));

  return async function callFas(payload) {
    const spawn = await spawned();
    return new Promise((resolve, reject) => {
      const child = spawn(command, args, {
        cwd: payload.cwd || undefined,
        env,
        stdio: ["pipe", "pipe", "pipe"],
      });
      const timer = setTimeout(() => child.kill(), TIMEOUT_MS);
      let stdout = "";
      let stderr = "";
      child.stdout.setEncoding("utf8");
      child.stderr.setEncoding("utf8");
      child.stdout.on("data", (chunk) => (stdout += chunk));
      child.stderr.on("data", (chunk) => (stderr += chunk));
      child.on("error", (error) => {
        clearTimeout(timer);
        reject(error);
      });
      child.on("close", (code) => {
        clearTimeout(timer);
        if (code !== 0) {
          reject(new Error(stderr.trim() || `fas exited with ${code}`));
          return;
        }
        try {
          resolve(JSON.parse(stdout));
        } catch (error) {
          reject(new Error(`fas returned invalid JSON: ${error.message}`));
        }
      });
      child.stdin.on("error", () => {});
      child.stdin.end(JSON.stringify(payload));
    });
  };
}

function replaceInput(input, updated) {
  for (const key of Object.keys(input)) delete input[key];
  return Object.assign(input, updated);
}

export async function decide(response, event, context) {
  const updated = response?.updated_input;
  if (response?.decision === "deny") return { block: true, reason: response.reason };
  if (response?.decision === "ask") {
    const approved = context?.hasUI
      ? await context.ui.confirm("fas", response.reason)
      : false;
    if (!approved) return { block: true, reason: response.reason };
  }
  if (updated && typeof updated === "object" && event?.input) {
    return { block: false, input: replaceInput(event.input, updated) };
  }
  return undefined;
}

function report(context, error) {
  if (context?.hasUI) context.ui.notify(`fas: ${error.message}`, "warning");
}

export function createFasHooks(options = {}) {
  const callFas = createFasCaller(options);

  return function fasHooks(pi) {
    if (pi[LOADED]) return;
    pi[LOADED] = true;

    const base = (event, context) => ({
      tool_name: event?.toolName,
      tool_input: event?.input,
      tool_call_id: event?.toolCallId,
      session_id: context?.sessionManager?.getSessionId?.(),
      cwd: context?.cwd,
    });

    pi.on("tool_call", async (event, context) => {
      let response;
      try {
        response = await callFas({ hook_event_name: "PreToolUse", ...base(event, context) });
      } catch (error) {
        report(context, error);
        return undefined;
      }
      return decide(response, event, context);
    });

    pi.on("tool_result", async (event, context) => {
      let response;
      try {
        response = await callFas({
          hook_event_name: "PostToolUse",
          ...base(event, context),
          tool_response: { content: event?.content, is_error: event?.isError },
        });
      } catch (error) {
        report(context, error);
        return undefined;
      }
      const text = response?.additional_context;
      if (typeof text !== "string" || text === "") return undefined;
      return { content: [...(event?.content ?? []), { type: "text", text }] };
    });
  };
}
