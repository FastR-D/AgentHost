import assert from "node:assert/strict";
import { createServer } from "node:http";
import { once } from "node:events";
import test from "node:test";
import { api } from "../src/api.ts";

test("log client reads all HTTP pages and rejects incomplete history", async () => {
  const requested: number[] = [];
  let failTail = false;
  const entries = Array.from({ length: 6001 }, (_, index) => ({ seq: index + 1, runId: "run", source: "host", time: "2026-11-17T00:00:00Z", text: `line ${index + 1}` }));
  const server = createServer((request, response) => {
    const url = new URL(request.url!, "http://fixture.test");
    const after = Number(url.searchParams.get("after"));
    requested.push(after);
    response.setHeader("Content-Type", "application/json");
    if (failTail && after > 0) {
      response.writeHead(503);
      response.end(JSON.stringify({ error: "tail unavailable" }));
    } else response.end(JSON.stringify(entries.filter((entry) => entry.seq > after).slice(0, 5000)));
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const origin = `http://127.0.0.1:${(server.address() as { port: number }).port}`;
  const fetch = globalThis.fetch;
  // Point the client's normal HTTP boundary at an isolated server.
  globalThis.fetch = (input, init) => fetch(new URL(String(input), origin), init);
  try {
    const logs = await api.logs("run");
    assert.equal(logs.length, 6001);
    assert.equal(logs.at(-1)?.text, "line 6001");
    assert.deepEqual(requested, [0, 5000]);
    assert.equal((await api.logs("run", 6001)).length, 0);
    failTail = true;
    await assert.rejects(api.logs("run"), /tail unavailable/);
  } finally {
    globalThis.fetch = fetch;
    server.closeAllConnections();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
});
