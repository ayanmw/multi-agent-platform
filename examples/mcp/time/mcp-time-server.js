// Minimal MCP 1.0 stdio server example: exposes get_current_time.
//
// Run with: node mcp-time-server.js
//
// This server intentionally implements the JSON-RPC wire protocol directly
// without the SDK so it is easy to read and copy for learning purposes.

import { readFileSync } from 'fs';

const serverInfo = {
  name: 'time-example',
  version: '1.0.0',
};

const capabilities = {};

function send(message) {
  process.stdout.write(JSON.stringify(message) + '\n');
}

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

function handleToolsList(id) {
  send({
    jsonrpc: '2.0',
    id,
    result: {
      tools: [
        {
          name: 'get_current_time',
          description: 'Returns the current server time in ISO 8601 format.',
          inputSchema: {
            type: 'object',
            properties: {
              timezone: {
                type: 'string',
                description: 'Optional IANA timezone (default: UTC).',
              },
            },
          },
        },
      ],
    },
  });
}

function handleToolCall(id, params) {
  const name = params.name;
  const args = params.arguments || {};
  if (name !== 'get_current_time') {
    send({ jsonrpc: '2.0', id, error: { code: -32601, message: 'Method not found' } });
    return;
  }

  const tz = args.timezone || 'UTC';
  try {
    const now = new Date();
    // Use Intl.DateTimeFormat if an explicit timezone is given.
    const formatter = new Intl.DateTimeFormat('en-US', {
      timeZone: tz,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
      timeZoneName: 'short',
    });
    const parts = formatter.formatToParts(now);
    const p = (type) => parts.find((x) => x.type === type)?.value;
    const isoLike = `${p('year')}-${p('month')}-${p('day')}T${p('hour')}:${p('minute')}:${p('second')} ${p('timeZoneName')}`;

    send({
      jsonrpc: '2.0',
      id,
      result: {
        content: [{ type: 'text', text: isoLike }],
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
  buffer = lines.pop(); // keep incomplete line for next chunk
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
