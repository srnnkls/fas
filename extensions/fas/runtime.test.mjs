import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { createFasCaller, createFasHooks, decide } from "./runtime.mjs";

function fakeFas(script) {
  const dir = mkdtempSync(join(tmpdir(), "fas-ext-"));
  const bin = join(dir, "fas");
  writeFileSync(bin, `#!/bin/sh\n${script}\n`);
  chmodSync(bin, 0o755);
  return { ...process.env, FAS_BIN: bin, FAS_STDIN: join(dir, "stdin") };
}

function fakePi() {
  const handlers = {};
  return { handlers, on: (name, handler) => (handlers[name] = handler) };
}

const ui = (answer) => {
  const calls = [];
  return {
    calls,
    hasUI: true,
    cwd: tmpdir(),
    ui: {
      confirm: async (...args) => (calls.push(["confirm", ...args]), answer),
      notify: (...args) => calls.push(["notify", ...args]),
    },
  };
};

test("decide maps deny to block", async () => {
  assert.deepEqual(await decide({ decision: "deny", reason: "no" }, {}, ui(true)), { block: true, reason: "no" });
});

test("decide allows on empty response", async () => {
  assert.equal(await decide({}, { input: { command: "ls" } }, ui(true)), undefined);
});

test("decide asks and honors approval", async () => {
  const context = ui(true);
  assert.equal(await decide({ decision: "ask", reason: "sure?" }, {}, context), undefined);
  assert.deepEqual(context.calls, [["confirm", "fas", "sure?"]]);
});

test("decide blocks declined ask", async () => {
  assert.deepEqual(await decide({ decision: "ask", reason: "sure?" }, {}, ui(false)), { block: true, reason: "sure?" });
});

test("decide blocks ask without UI", async () => {
  assert.deepEqual(await decide({ decision: "ask", reason: "sure?" }, {}, { hasUI: false }), { block: true, reason: "sure?" });
});

test("decide replaces input in place", async () => {
  const event = { input: { command: "rm x", timeout: 5 } };
  const original = event.input;
  const result = await decide({ decision: "allow", updated_input: { command: "ls" } }, event, ui(true));
  assert.deepEqual(result, { block: false, input: { command: "ls" } });
  assert.equal(result.input, original);
});

test("decide skips rewrite when ask is declined", async () => {
  const event = { input: { command: "rm x" } };
  await decide({ decision: "ask", reason: "r", updated_input: { command: "ls" } }, event, ui(false));
  assert.deepEqual(event.input, { command: "rm x" });
});

test("caller pipes payload and parses response", async () => {
  const env = fakeFas('cat > "$FAS_STDIN"; printf \'%s\' "$*" >> "$FAS_STDIN.args"; echo \'{"decision":"deny","reason":"no"}\'');
  const payload = { hook_event_name: "PreToolUse", tool_name: "bash", cwd: tmpdir() };
  const response = await createFasCaller({ env })(payload);
  assert.deepEqual(response, { decision: "deny", reason: "no" });
  assert.deepEqual(JSON.parse(readFileSync(env.FAS_STDIN, "utf8")), payload);
  assert.equal(readFileSync(`${env.FAS_STDIN}.args`, "utf8"), "eval --harness pi");
});

test("caller rejects non-zero exit with stderr", async () => {
  const env = fakeFas('echo "rule load failed" >&2; exit 1');
  await assert.rejects(createFasCaller({ env })({}), /rule load failed/);
});

test("caller rejects invalid JSON", async () => {
  const env = fakeFas("echo nope");
  await assert.rejects(createFasCaller({ env })({}), /invalid JSON/);
});

test("caller rejects missing binary", async () => {
  const env = { ...process.env, FAS_BIN: join(tmpdir(), "fas-missing-bin") };
  await assert.rejects(createFasCaller({ env })({}));
});

test("tool_call forwards host event and blocks on deny", async () => {
  const env = fakeFas('cat > "$FAS_STDIN"; echo \'{"decision":"deny","reason":"no"}\'');
  const pi = fakePi();
  createFasHooks({ env })(pi);
  const context = { ...ui(true), sessionManager: { getSessionId: () => "s1" } };
  const result = await pi.handlers.tool_call({ toolName: "bash", toolCallId: "c1", input: { command: "rm -rf /" } }, context);
  assert.deepEqual(result, { block: true, reason: "no" });
  assert.deepEqual(JSON.parse(readFileSync(env.FAS_STDIN, "utf8")), {
    hook_event_name: "PreToolUse",
    tool_name: "bash",
    tool_input: { command: "rm -rf /" },
    tool_call_id: "c1",
    session_id: "s1",
    cwd: tmpdir(),
  });
});

test("tool_call fails open with a notice", async () => {
  const env = fakeFas("exit 3");
  const pi = fakePi();
  createFasHooks({ env })(pi);
  const context = ui(true);
  assert.equal(await pi.handlers.tool_call({ toolName: "bash", input: {} }, context), undefined);
  assert.equal(context.calls[0][0], "notify");
});

test("tool_result appends additional context", async () => {
  const env = fakeFas('cat >/dev/null; echo \'{"additional_context":"note"}\'');
  const pi = fakePi();
  createFasHooks({ env })(pi);
  const content = [{ type: "text", text: "out" }];
  const result = await pi.handlers.tool_result({ toolName: "bash", input: {}, content }, ui(true));
  assert.deepEqual(result, { content: [...content, { type: "text", text: "note" }] });
});

test("hooks register once per host", () => {
  const pi = fakePi();
  let count = 0;
  pi.on = () => count++;
  const hooks = createFasHooks();
  hooks(pi);
  hooks(pi);
  assert.equal(count, 2);
});
