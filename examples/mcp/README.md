# MCP Server Examples

This directory contains minimal [Model Context Protocol](https://modelcontextprotocol.io) (MCP) servers written in plain Node.js. They implement the JSON-RPC wire protocol directly so they are easy to read and adapt.

## Included Examples

| Server | Tools | Description |
|--------|-------|-------------|
| `time/` | `get_current_time` | Returns the current time, optionally in a given IANA timezone. |
| `calc/` | `add`, `subtract`, `multiply`, `divide` | Basic arithmetic on two numbers. |

## Run a Server Manually

```bash
cd examples/mcp/time
node mcp-time-server.js
```

The server reads newline-delimited JSON-RPC from stdin and writes responses to stdout. You can send requests manually:

```text
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"cli","version":"1.0"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_current_time","arguments":{"timezone":"Asia/Shanghai"}}}
```

## Static Configuration

Add an MCP server to the platform at startup via the `MCP_SERVERS` environment variable:

```bash
export MCP_SERVERS='[
  {"name":"time","transport":"stdio","command":"node","args":["examples/mcp/time/mcp-time-server.js"],"enabled":true},
  {"name":"calc","transport":"stdio","command":"node","args":["examples/mcp/calc/mcp-calc-server.js"],"enabled":true}
]'
go run ./cmd/server
```

When the server starts, the MCP manager connects to each enabled server, lists its tools, and registers them under the namespace `mcp__<server>__<tool>`. The calc `add` tool therefore becomes `mcp__calc__add`.

## Dynamic API

You can also add, enable, disable, or remove MCP servers at runtime. Dynamic servers are persisted to the `mcp_servers` SQLite table and survive restarts.

```bash
curl -X POST http://localhost:8080/api/mcp/servers \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "local-time",
    "config": {
      "name": "local-time",
      "transport": "stdio",
      "command": "node",
      "args": ["examples/mcp/time/mcp-time-server.js"],
      "enabled": true
    },
    "enabled": true
  }'
```

Other endpoints:

- `GET /api/mcp/servers` — list managed servers and their load status
- `POST /api/mcp/servers/:id/enable`
- `POST /api/mcp/servers/:id/disable`
- `DELETE /api/mcp/servers/:id`

> Note: Servers loaded from `MCP_SERVERS` are marked as static. They can be enabled or disabled at runtime, but cannot be deleted via the API.

## Writing Your Own Server

1. Create a new directory under `examples/mcp/<name>/`.
2. Implement the three required JSON-RPC methods:
   - `initialize`
   - `tools/list`
   - `tools/call`
3. Add a `package.json` for discoverability.
4. Register the server through `MCP_SERVERS` or the API.

See `time/mcp-time-server.js` for the smallest complete example.
