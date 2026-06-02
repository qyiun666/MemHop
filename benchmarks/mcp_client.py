#!/usr/bin/env python3
"""MemHop MCP Client — JSON-RPC 2.0 over Unix Domain Socket (v0.13.1).

Usage:
    from benchmarks.mcp_client import MemHopMCPClient

    mcp = MemHopMCPClient("target/release/memhop-mcp-server")
    mcp.start_reader()
    for doc in docs:
        mcp.store(doc["text"], agent_id="bench", session_id="default")
    results = mcp.recall("query text", agent_id="bench", limit=10)
    mcp.close()
"""

import json
import os
import select
import socket
import subprocess
import sys
import time
from typing import Any, Optional

DEFAULT_RECV_TIMEOUT = 120


class MemHopMCPClient:
    """MCP JSON-RPC 2.0 client over Unix Domain Socket for MemHop benchmark (v0.13.1)."""

    def __init__(
        self,
        binary_path: str,
        socket_path: str = "",
        env_extra: Optional[dict[str, str]] = None,
        recv_timeout: float = DEFAULT_RECV_TIMEOUT,
    ):
        if not os.path.exists(binary_path):
            raise FileNotFoundError(
                f"MCP server binary not found at {binary_path}. "
                "Build with: cargo build --release"
            )
        self.binary = binary_path
        self.socket_path = socket_path or "/tmp/memhop_bench.sock"
        self.env_extra = env_extra or {}
        self.recv_timeout = recv_timeout
        self._proc: Optional[subprocess.Popen] = None
        self._sock: Optional[socket.socket] = None
        self._reader: Any = None  # sock.makefile()
        self._req_id: int = 0

    # ── lifecycle ──────────────────────────────────────────

    def start_reader(self):
        """Start MCP server subprocess and initialize via Unix socket."""
        if self._sock is not None:
            return
        env = os.environ.copy()
        env.update(self.env_extra)
        if sys.platform == "darwin":
            current = env.get("ORT_DYLIB_PATH", "")
            if not current or not os.path.exists(current):
                for candidate in [
                    "/usr/local/lib/libonnxruntime.dylib",
                    "/opt/homebrew/lib/libonnxruntime.dylib",
                ]:
                    if os.path.exists(candidate):
                        env["ORT_DYLIB_PATH"] = candidate
                        break

        # Ensure socket directory exists
        socket_dir = os.path.dirname(self.socket_path)
        if socket_dir:
            os.makedirs(socket_dir, exist_ok=True)

        # Clean up leftover socket file
        try:
            os.unlink(self.socket_path)
        except FileNotFoundError:
            pass

        self._proc = subprocess.Popen(
            [self.binary, f"--socket-path={self.socket_path}"],
            stderr=subprocess.PIPE, env=env, text=True,
        )

        print("  MCP: connecting to socket...", end=" ", flush=True)
        self._sock = self._wait_for_socket(timeout=30)
        self._reader = self._sock.makefile("r", buffering=1)
        print("OK", flush=True)

        print("  MCP: initializing...", end=" ", flush=True)
        resp = self._call("initialize", {"protocolVersion": "2024-11-05"})
        if "error" in resp:
            raise RuntimeError(f"MCP initialize failed: {resp['error']}")
        print("OK", flush=True)
        self._notify("notifications/initialized", {})
        print("  MCP: server ready", flush=True)

    def _wait_for_socket(self, timeout: float) -> socket.socket:
        """Poll for the Unix socket to appear and connect."""
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                sock.connect(self.socket_path)
                sock.settimeout(self.recv_timeout)
                return sock
            except (FileNotFoundError, ConnectionRefusedError):
                time.sleep(0.1)
            except OSError:
                time.sleep(0.1)
        raise RuntimeError(
            f"MCP server socket not ready after {timeout}s: {self.socket_path}"
        )

    def close(self):
        """Stop MCP server."""
        if self._reader:
            try:
                self._reader.close()
            except Exception:
                pass
            self._reader = None
        if self._sock:
            try:
                self._sock.close()
            except Exception:
                pass
            self._sock = None
        if self._proc:
            try:
                self._proc.terminate()
                self._proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self._proc.kill()
                self._proc.wait()
            self._proc = None

    # ── tools (v0.13) ──────────────────────────────────────

    def store(
        self,
        text: str,
        agent_id: str = "",
        session_id: str = "bench",
        turn_id: str = "",
        turn_index: int = 0,
        topic_label: Optional[str] = None,
        valence: float = 0.0,
        arousal: float = 0.5,
        kind: str = "episode",
        tree_id: str = "",
        vector: Optional[list[float]] = None,
        agent_response: Optional[str] = None,
        auto_create_tree: Optional[bool] = None,
        match_threshold: Optional[float] = None,
        context_half_life: Optional[float] = None,
        auto_compress: Optional[bool] = None,
        llm_compressed_summary: Optional[str] = None,
        llm_keywords: Optional[list[str]] = None,
    ) -> dict:
        """Store a perception/memory (v0.13)."""
        args: dict[str, Any] = {
            "agent_id": agent_id, "text": text,
            "session_id": session_id, "turn_id": turn_id, "turn_index": turn_index,
            "valence": valence, "arousal": arousal, "kind": kind,
        }
        if topic_label: args["topic_label"] = topic_label
        if tree_id: args["tree_id"] = tree_id
        if vector: args["vector"] = vector
        if agent_response is not None: args["agent_response"] = agent_response
        if auto_create_tree is not None: args["auto_create_tree"] = auto_create_tree
        if match_threshold is not None: args["match_threshold"] = match_threshold
        if context_half_life is not None: args["context_half_life"] = context_half_life
        if auto_compress is not None: args["auto_compress"] = auto_compress
        if llm_compressed_summary is not None: args["llm_compressed_summary"] = llm_compressed_summary
        if llm_keywords is not None: args["llm_keywords"] = llm_keywords

        result = self._tool_call("memhop_store", args)
        if isinstance(result, dict) and "engram_id" in result and "memory_id" not in result:
            result["memory_id"] = result["engram_id"]
        return result

    def store_knowledge(
        self, text: str, tree_path: str,
        source_path: str = "", source_textunit: str = "",
        agent_id: str = "",
    ) -> dict:
        """Store a knowledge chunk (v0.13)."""
        args: dict[str, Any] = {"agent_id": agent_id, "text": text, "kind": "knowledge", "tree_path": tree_path}
        if source_path: args["source_path"] = source_path
        if source_textunit: args["source_textunit"] = source_textunit
        result = self._tool_call("memhop_store", args)
        if isinstance(result, dict) and "engram_id" in result and "memory_id" not in result:
            result["memory_id"] = result["engram_id"]
        return result

    def recall(
        self,
        query: str,
        agent_id: str = "",
        session_id: str = "",
        limit: int = 5,
        mode: str = "retrieval",
        use_reranker: bool = True,
        kind_filter: Optional[list[str]] = None,
        tree: Optional[str] = None,
        query_vector: Optional[list[float]] = None,
        context_id: Optional[str] = None,
        use_worldview_filter: Optional[bool] = None,
        llm_conflict_check: Optional[str] = None,
    ) -> dict:
        """Recall memories (v0.13)."""
        args: dict[str, Any] = {
            "agent_id": agent_id, "query": query, "session_id": session_id,
            "limit": limit, "mode": mode, "use_reranker": use_reranker,
        }
        if kind_filter: args["kind_filter"] = kind_filter
        if tree: args["tree"] = tree
        if query_vector is not None: args["query_vector"] = query_vector
        if context_id is not None: args["context_id"] = context_id
        if use_worldview_filter is not None: args["use_worldview_filter"] = use_worldview_filter
        if llm_conflict_check is not None: args["llm_conflict_check"] = llm_conflict_check
        return self._tool_call("memhop_recall", args)

    def dream(
        self,
        agent_id: str = "",
        context_compress: Optional[bool] = None,
        llm_patterns: Optional[list[dict]] = None,
        llm_contradictions: Optional[list[dict]] = None,
    ) -> dict:
        """Run Dream consolidation (v0.13)."""
        args: dict[str, Any] = {"agent_id": agent_id}
        if context_compress is not None: args["context_compress"] = context_compress
        if llm_patterns is not None: args["llm_patterns"] = llm_patterns
        if llm_contradictions is not None: args["llm_contradictions"] = llm_contradictions
        return self._tool_call("memhop_dream", args) or {}

    def stats(self, agent_id: str = "") -> dict:
        """Get brain statistics (v0.13)."""
        return self._tool_call("memhop_stats", {"agent_id": agent_id})

    def count(self, agent_id: str = "") -> int:
        raw = self._tool_call("memhop_count", {"agent_id": agent_id})
        unwrapped = self._unwrap_content(raw)
        if isinstance(unwrapped, dict):
            return unwrapped.get("count", unwrapped.get("total", 0))
        return 0

    # ── helpers ────────────────────────────────────────────

    @staticmethod
    def _unwrap_content(result: Any) -> Any:
        if isinstance(result, dict) and "content" in result:
            for item in result["content"]:
                if isinstance(item, dict) and item.get("type") == "text":
                    try:
                        return json.loads(item["text"])
                    except (json.JSONDecodeError, TypeError):
                        return item["text"]
        return result

    # ── JSON-RPC internals ─────────────────────────────────

    def _next_id(self) -> int:
        self._req_id += 1
        return self._req_id

    def _call(self, method: str, params: dict) -> dict:
        return self._send({"jsonrpc": "2.0", "id": self._next_id(), "method": method, "params": params})

    def _notify(self, method: str, params: dict):
        self._send_raw({"jsonrpc": "2.0", "method": method, "params": params})

    def _tool_call(self, name: str, arguments: dict) -> dict:
        return self._call("tools/call", {"name": name, "arguments": arguments})

    def _send(self, req: dict) -> dict:
        self._send_raw(req)
        return self._recv()

    def _send_raw(self, req: dict):
        if self._sock is None:
            raise RuntimeError("MCP server not connected")
        data = (json.dumps(req, ensure_ascii=False) + "\n").encode("utf-8")
        self._sock.sendall(data)

    def _recv(self) -> dict:
        if self._reader is None or self._sock is None:
            raise RuntimeError("MCP server not connected")
        deadline = time.time() + self.recv_timeout
        while True:
            remaining = deadline - time.time()
            if remaining <= 0:
                raise RuntimeError(f"MCP server timed out after {self.recv_timeout:.0f}s")
            r, _, _ = select.select([self._sock], [], [], min(remaining, 5.0))
            if not r:
                continue
            line = self._reader.readline()
            if not line:
                raise RuntimeError("MCP server closed connection")
            line = line.strip()
            if not line:
                continue
            try:
                resp = json.loads(line)
            except json.JSONDecodeError:
                continue
            if "error" in resp:
                raise RuntimeError(f"MCP error {resp['error'].get('code')}: {resp['error'].get('message')}")
            return resp.get("result", resp)
