const http = require('http');
const { setTimeout: sleep } = require('node:timers/promises');

function probe(port, proof) {
  return new Promise((resolve) => {
    const req = http.get({
      hostname: '127.0.0.1',
      port,
      path: '/api/readyz',
      agent: false,
      signal: AbortSignal.timeout(2000)
    }, (response) => {
      const matches = response.statusCode === 200 &&
        response.headers['x-newapi-desktop-startup'] === proof;
      response.destroy();
      resolve(matches);
    });
    req.once('error', () => resolve(false));
  });
}

async function waitForPackagedBackend(port, proof, child, attempts = 30, retryDelay = 1000) {
  let childExited = false;
  const markExited = () => { childExited = true; };
  child.once('exit', markExited);
  child.once('error', markExited);
  const stopped = () => childExited || child.exitCode != null || child.signalCode != null;
  try {
    for (let attempt = 0; attempt < attempts; attempt++) {
      if (stopped()) break;
      if (await probe(port, proof) && !stopped()) return;
      if (attempt + 1 < attempts && !stopped()) await sleep(retryDelay);
    }
    throw new Error(`The packaged backend did not become ready on loopback port ${port}`);
  } finally {
    child.removeListener('exit', markExited);
    child.removeListener('error', markExited);
  }
}

module.exports = { waitForPackagedBackend };
