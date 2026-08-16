// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Custom stdio transport for the MCP server.
//
// It replaces mcp.StdioTransport because the SDK's built-in ioConn read loop
// (go-sdk v1.7.0) has a message-dropping bug: after decoding each message it
// calls json.Decoder.Buffered().Read to check for trailing data. When the
// decoder buffer is empty that call blocks waiting for the next byte from
// stdin, and if the client's next message arrives later (any interactive
// client that awaits responses), the first byte of that message ('{') is
// misread as invalid trailing data, the connection errors out, and every
// subsequent request is silently dropped.
//
// This transport keeps the SDK's single-reader-goroutine architecture
// (messages flow through an incoming channel) but reads stdin line-by-line
// with bufio.Scanner (newline-delimited JSON per the MCP stdio spec), which
// has no cross-message buffering, so the trailing-data misread cannot occur.
//
// Note: it deliberately does NOT implement mcp.ProtocolVersionSupporter.
// The SDK client (v1.7.0) first probes server/discover and, when the server
// only advertises pre-2026-07-28 versions, falls back to a legacy initialize
// that the server rejects as a duplicate (discover already populated the
// session state). Advertising all SDK versions keeps clients on the
// sessionless 2026-07-28 flow, which works end to end.

package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxLineBytes caps a single stdio line; tool arguments and results can be
// large (e.g. batch knowledge imports), so the scanner buffer is generous.
const maxLineBytes = 16 * 1024 * 1024

// lineTransport is a newline-delimited JSON transport over os.Stdin/stdout.
type lineTransport struct{}

// stdioRW combines os.Stdin and os.Stdout into one ReadWriteCloser. Close
// only closes stdin (unblocking the scanner); stdout stays open for any
// remaining protocol output until the process exits.
type stdioRW struct {
	io.Reader
	io.Writer
}

func (stdioRW) Close() error { return os.Stdin.Close() }

// Connect implements mcp.Transport.
func (lineTransport) Connect(context.Context) (mcp.Connection, error) {
	return newLineConn(stdioRW{os.Stdin, os.Stdout}), nil
}

// msgOrErr is one decoded line: a message or the read-loop error.
type msgOrErr struct {
	msg jsonrpc.Message
	err error
}

// lineConn is the mcp.Connection implementation behind lineTransport.
// A single read goroutine scans stdin line-by-line and feeds incoming; Read
// pulls from that channel. This mirrors the SDK's ioConn architecture minus
// the trailing-data misread.
type lineConn struct {
	rwc      io.ReadWriteCloser
	incoming chan msgOrErr
	closed   chan struct{}
	closeErr error
	closeOne sync.Once
	writeMu  sync.Mutex
}

func newLineConn(rwc io.ReadWriteCloser) *lineConn {
	c := &lineConn{
		rwc:      rwc,
		incoming: make(chan msgOrErr),
		closed:   make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *lineConn) readLoop() {
	sc := bufio.NewScanner(c.rwc)
	sc.Buffer(make([]byte, 64*1024), maxLineBytes)
	for {
		if !sc.Scan() {
			err := sc.Err()
			if err == nil {
				err = io.EOF
			}
			select {
			case c.incoming <- msgOrErr{err: err}:
			case <-c.closed:
			}
			return
		}
		msg, err := jsonrpc.DecodeMessage(sc.Bytes())
		select {
		case c.incoming <- msgOrErr{msg: msg, err: err}:
		case <-c.closed:
			return
		}
	}
}

// Read implements mcp.Connection.
func (c *lineConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	select {
	case v := <-c.incoming:
		return v.msg, v.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, io.EOF
	}
}

// Write implements mcp.Connection; safe for concurrent use.
func (c *lineConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.rwc.Write(data)
	return err
}

// Close implements mcp.Connection; idempotent and unblocks pending Reads.
func (c *lineConn) Close() error {
	c.closeOne.Do(func() {
		close(c.closed)
		// Closing stdin unblocks the scanner; the process exits right after.
		c.closeErr = c.rwc.Close()
	})
	return c.closeErr
}

// SessionID implements mcp.Connection.
func (c *lineConn) SessionID() string { return "" }
