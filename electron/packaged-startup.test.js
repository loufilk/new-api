const assert = require('node:assert/strict');
const { EventEmitter, once } = require('node:events');
const http = require('node:http');
const { test } = require('node:test');
const { waitForPackagedBackend } = require('./packaged-startup');

const proof = 'a'.repeat(48);

async function listen(t, handler) {
  const server = http.createServer(handler);
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => server.close());
  return server.address().port;
}

test('refuses an unrelated 200 listener even when it answers the readiness path', async (t) => {
  const port = await listen(t, (req, res) => {
    assert.equal(req.url, '/api/readyz');
    res.writeHead(200).end('unrelated');
  });
  await assert.rejects(waitForPackagedBackend(port, proof, new EventEmitter(), 2, 1), /did not become ready/);
});

test('requires both the child proof and successful application readiness', async (t) => {
  let probes = 0;
  const port = await listen(t, (_req, res) => {
    probes++;
    res.writeHead(probes === 1 ? 503 : 200, { 'X-NewAPI-Desktop-Startup': proof }).end();
  });
  await waitForPackagedBackend(port, proof, new EventEmitter(), 3, 1);
  assert.equal(probes, 2);
});

test('does not navigate when the packaged process exits while the probe is in flight', async (t) => {
  const child = new EventEmitter();
  const port = await listen(t, (_req, res) => {
    child.emit('exit', 1);
    res.writeHead(200, { 'X-NewAPI-Desktop-Startup': proof }).end();
  });
  await assert.rejects(waitForPackagedBackend(port, proof, child, 3, 1), /did not become ready/);
  assert.equal(child.listenerCount('exit'), 0);
});
