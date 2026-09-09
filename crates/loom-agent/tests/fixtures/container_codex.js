#!/usr/bin/env node
// Deterministic provider fixture, used only in the Docker conformance proof.
const fs = require('node:fs');
const readline = require('node:readline');
const marker = '/session/thread-fixture';
const send = (message) => process.stdout.write(JSON.stringify(message) + '\n');
let thread;
const scenario = fs.existsSync('/workspace/scenario') ? fs.readFileSync('/workspace/scenario', 'utf8') : '';
readline.createInterface({ input: process.stdin }).on('line', (line) => {
  const request = JSON.parse(line);
  const reply = (result) => send({ id: request.id, result });
  if (request.method === 'initialize') return reply({});
  if (request.method === 'initialized') return;
  if (request.method === 'thread/start') {
    if (fs.existsSync(marker)) throw new Error('Replacement thread forbidden');
    thread = 'contained-thread-' + require('node:crypto').randomUUID();
    fs.writeFileSync(marker, JSON.stringify({ thread, turns: 0 }));
    return reply({ thread: { id: thread } });
  }
  if (request.method === 'thread/resume') {
    const saved = JSON.parse(fs.readFileSync(marker));
    if (saved.thread !== request.params.threadId || saved.turns !== 1) throw new Error('Wrong continuity');
    thread = saved.thread;
    return reply({ thread: { id: scenario === 'wrong-thread' ? 'unrelated-thread' : thread } });
  }
  if (request.method === 'turn/start') {
    const saved = JSON.parse(fs.readFileSync(marker));
    if (saved.thread !== request.params.threadId) throw new Error('Wrong thread');
    const turnId = 'turn-' + saved.turns;
    const status = saved.turns === 0 ? 'yield' : 'complete';
    saved.turns++;
    fs.writeFileSync(marker, JSON.stringify(saved));
    if (scenario === 'escaped-child') {
      const child = require('node:child_process').spawn(process.execPath, ['-e',
        "setInterval(()=>require('node:fs').writeFileSync('/session/child-heartbeat', String(Date.now())),20)"],
        { detached: true, stdio: 'ignore' });
      child.unref();
    }
    reply({ turn: { id: turnId } });
    send({ method: 'item/completed', params: { threadId: thread, turnId,
      item: { type: 'agentMessage', text: scenario === 'malformed' ? 'invalid JSON' : JSON.stringify({ status, next_wait: null }) } } });
    send({ method: 'turn/completed', params: { threadId: thread, turn: { id: turnId, status: 'completed' } } });
    return;
  }
  throw new Error('Unexpected request');
});
