package mcp

import (
    "bytes"
    "io"
    "testing"
    "time"
)

func TestStdioTransportRoundTrip(t *testing.T) {
    // 通过手动接通 stdin/stdout 来模拟一个子进程。
    inR, inW := io.Pipe()
    outR, outW := io.Pipe()

    tr := &stdioTransport{stdin: inW, stdout: outR, stderr: nil}
    go func() {
        // 读取请求行，加换行框架后回显。
        buf := make([]byte, 1024)
        n, _ := inR.Read(buf)
        outW.Write(buf[:n])
        outW.Close()
    }()

    sent := []byte(`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}`)
    if err := tr.Send(sent); err != nil {
        t.Fatalf("send: %v", err)
    }

    got, err := tr.Receive(time.Second)
    if err != nil {
        t.Fatalf("receive: %v", err)
    }
    if !bytes.Equal(bytes.TrimSpace(got), sent) {
        t.Fatalf("expected %s, got %s", sent, got)
    }
}
