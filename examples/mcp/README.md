# MCP Server 示例

本目录包含用纯 Node.js 编写的最小化 [Model Context Protocol](https://modelcontextprotocol.io)（MCP）server。它们直接实现 JSON-RPC 线协议，因此易于阅读和改造。

## 内置示例

| Server | Tools | 描述 |
|--------|-------|------|
| `time/` | `get_current_time` | 返回当前时间，可选传入 IANA 时区。 |
| `calc/` | `add`、`subtract`、`multiply`、`divide` | 对两个数字执行基本算术运算。 |

## 手动运行 Server

```bash
cd examples/mcp/time
node mcp-time-server.js
```

Server 从 stdin 读取按行分隔的 JSON-RPC 请求，并将响应写入 stdout。你可以手动发送请求：

```text
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"cli","version":"1.0"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_current_time","arguments":{"timezone":"Asia/Shanghai"}}}
```

## 静态配置

通过 `MCP_SERVERS` 环境变量在平台启动时添加 MCP server：

```bash
export MCP_SERVERS='[
  {"name":"time","transport":"stdio","command":"node","args":["examples/mcp/time/mcp-time-server.js"],"enabled":true},
  {"name":"calc","transport":"stdio","command":"node","args":["examples/mcp/calc/mcp-calc-server.js"],"enabled":true}
]'
go run ./cmd/server
```

Server 启动时，MCP manager 会连接每个已启用的 server，列出其工具，并以 `mcp__<server>__<tool>` 的命名空间注册。因此 calc 的 `add` 工具会被注册为 `mcp__calc__add`。

## 动态 API

你也可以在运行时添加、启用、禁用或删除 MCP server。动态 server 会持久化到 `mcp_servers` SQLite 表，重启后仍然保留。

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

其他 endpoint：

- `GET /api/mcp/servers` — 列出受管理的 server 及其加载状态
- `POST /api/mcp/servers/:id/enable`
- `POST /api/mcp/servers/:id/disable`
- `DELETE /api/mcp/servers/:id`

> 注意：通过 `MCP_SERVERS` 加载的 server 会被标记为静态 server。它们可以在运行时启用或禁用，但无法通过 API 删除。

## 编写自己的 Server

1. 在 `examples/mcp/<name>/` 下新建一个目录。
2. 实现以下三个必需的 JSON-RPC 方法：
   - `initialize`
   - `tools/list`
   - `tools/call`
3. 添加一个 `package.json` 以便于发现。
4. 通过 `MCP_SERVERS` 或 API 注册该 server。

最小且完整的示例请参见 `time/mcp-time-server.js`。
