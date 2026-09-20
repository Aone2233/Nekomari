import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

// Exercise the real client without a browser or a live server.
const exports = {};
const source = readFileSync(new URL('../src/lib/rpc2.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
const states = { DISCONNECTED: 'disconnected', CONNECTED: 'connected' };
vm.runInNewContext(compiled, {
  exports,
  require(name) {
    if (name === '../types/rpc2') return { RPC2ConnectionState: states };
    if (name === '../i18n/config') return { default: { t: value => value } };
    throw new Error(`Unexpected import: ${name}`);
  },
});

for (const reason of ['invalid params', 'request timed out', 'connection closed']) {
  test(`does not replay a sent operation after ${reason}`, async () => {
    const client = new exports.RPC2Client('/api/rpc2', { autoConnect: false });
    client.connectionState = states.CONNECTED;
    let wsCalls = 0;
    let httpCalls = 0;
    client.callViaWebSocket = async () => { wsCalls++; throw new Error(reason); };
    client.callViaHTTP = async () => { httpCalls++; };
    await assert.rejects(client.call('admin:exec', {}), { message: reason });
    assert.equal(wsCalls, 1);
    assert.equal(httpCalls, 0);
  });
}

test('uses HTTP when no WebSocket request has been sent', async () => {
  const client = new exports.RPC2Client('/api/rpc2', { autoConnect: false });
  client.callViaHTTP = async () => 'ok';
  assert.equal(await client.call('public:getVersion'), 'ok');
});
