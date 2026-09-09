#!/usr/bin/env node
// Deterministic private-protocol fixture. Never invokes a model.
const readline = require('node:readline');

const emit = (value) => process.stdout.write(`${JSON.stringify(value)}\n`);

readline.createInterface({ input: process.stdin }).on('line', (line) => {
  const request = JSON.parse(line);
  const method = request.method;
  if (method === 'initialized') return;

  let result;
  if (method === 'initialize') {
    result = {};
  } else if (method === 'thread/start' || method === 'thread/resume') {
    if (request.params.sandbox !== 'read-only' || request.params.approvalPolicy !== 'never') {
      throw new Error('unsafe runtime policy');
    }
    let id = request.params.threadId ?? 'thread-fixture';
    if (id === 'wrong-resume') id = 'unrelated-context';
    result = { thread: { id } };
  } else if (method === 'turn/start') {
    // Completion intentionally precedes the RPC response to exercise buffering.
    const threadId = request.params.threadId;
    const text = request.params.input[0].text;
    if (text === 'approval') {
      emit({ id: 'server-request', method: 'item/commandExecution/requestApproval' });
      return;
    }
    emit({
      method: 'item/completed',
      params: {
        threadId,
        turnId: 'turn-1',
        item: { type: 'agentMessage', text: '{"ok":true}' },
      },
    });
    emit({
      method: 'turn/completed',
      params: { threadId, turn: { id: 'turn-1', status: 'completed' } },
    });
    if (text === 'lost-ack') process.exit(0);
    result = { turn: { id: 'turn-1' } };
  } else {
    throw new Error(`unexpected method: ${method}`);
  }
  emit({ id: request.id, result });
});
