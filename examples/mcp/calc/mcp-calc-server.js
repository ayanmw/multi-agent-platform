// Minimal MCP 1.0 stdio server example: exposes arithmetic operations.
//
// Run with: node mcp-calc-server.js
//
// Tools:
//   add      - add two numbers
//   subtract - subtract b from a
//   multiply - multiply two numbers
//   divide   - divide a by b

function send(message) {
  process.stdout.write(JSON.stringify(message) + '\n');
}

const serverInfo = {
  name: 'calc-example',
  version: '1.0.0',
};

const capabilities = {};

function handleInitialize(id) {
  send({
    jsonrpc: '2.0',
    id,
    result: {
      protocolVersion: '2024-11-05',
      serverInfo,
      capabilities,
    },
  });
}

const operations = {
  add: (a, b) => a + b,
  subtract: (a, b) => a - b,
  multiply: (a, b) => a * b,
  divide: (a, b) => {
    if (b === 0) throw new Error('division by zero');
    return a / b;
  },
};

function handleToolsList(id) {
  const tools = Object.keys(operations).map((name) => ({
    name,
    description: `Perform ${name} on two numbers a and b.`,
    inputSchema: {
      type: 'object',
      properties: {
        a: { type: 'number' },
        b: { type: 'number' },
      },
      required: ['a', 'b'],
    },
  }));
  send({
    jsonrpc: '2.0',
    id,
    result: { tools },
  });
}

function handleToolCall(id, params) {
  const name = params.name;
  const args = params.arguments || {};
  const op = operations[name];
  if (!op) {
    send({ jsonrpc: '2.0', id, error: { code: -32601, message: 'Tool not found: ' + name } });
    return;
  }
  try {
    const a = Number(args.a);
    const b = Number(args.b);
    if (Number.isNaN(a) || Number.isNaN(b)) {
      throw new Error('arguments must be numbers');
    }
    const result = op(a, b);
    send({
      jsonrpc: '2.0',
      id,
      result: {
        content: [{ type: 'text', text: String(result) }],
      },
    });
  } catch (err) {
    send({
      jsonrpc: '2.0',
      id,
      result: {
        content: [{ type: 'text', text: 'Error: ' + err.message }],
        isError: true,
      },
    });
  }
}

let buffer = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => {
  buffer += chunk;
  let lines = buffer.split('\n');
  buffer = lines.pop();
  for (const line of lines) {
    if (!line.trim()) continue;
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      continue;
    }
    if (msg.method === 'initialize') {
      handleInitialize(msg.id);
    } else if (msg.method === 'notifications/initialized') {
      // no response needed
    } else if (msg.method === 'tools/list') {
      handleToolsList(msg.id);
    } else if (msg.method === 'tools/call') {
      handleToolCall(msg.id, msg.params || {});
    }
  }
});

process.stdin.on('end', () => {
  process.exit(0);
});
