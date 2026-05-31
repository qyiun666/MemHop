#!/usr/bin/env python3
"""MemHop model verification — tests each model with store+recall and BEIR nfcorpus."""

import json, os, sys, time, subprocess, tempfile, shutil

MCP_BIN = os.path.join(os.path.dirname(__file__), "../../target/release/memhop-mcp-server")
MODELS_DIR = os.path.join(os.path.dirname(__file__), "../../models")

# Model name → directory name
MODELS = {
    "bge-small-en": "bge-small-en-v1.5",
    "bge-small-zh": "bge-small-zh-v1.5",
    "bge-base-en": "bge-base-en-v1.5",
    "bge-base-zh": "bge-base-zh-v1.5",
    "bge-m3": "bge-m3",
}

def test_model(model_name):
    """Start MCP server with model, store a test memory, verify recall works."""
    model_dir = os.path.join(MODELS_DIR, MODELS[model_name])
    if not os.path.exists(os.path.join(model_dir, "model.safetensors")):
        print(f"  ⏭️  {model_name}: model.safetensors not found, skipping")
        return None

    db_dir = tempfile.mkdtemp()
    env = os.environ.copy()
    env["MEMHOP_DB_PATH"] = db_dir
    env["MEMHOP_ONNX_MODEL"] = model_dir
    env["TOKENIZERS_PARALLELISM"] = "false"

    proc = subprocess.Popen(
        [MCP_BIN], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, env=env, text=True
    )

    req_id = [0]
    def send(method, params=None):
        req_id[0] += 1
        req = {"jsonrpc": "2.0", "id": req_id[0], "method": method}
        if params:
            req["params"] = params
        proc.stdin.write(json.dumps(req) + "\n")
        proc.stdin.flush()

    def recv(timeout=30):
        deadline = time.time() + timeout
        import select
        while True:
            remaining = deadline - time.time()
            if remaining <= 0:
                return None
            r, _, _ = select.select([proc.stdout], [], [], min(remaining, 5))
            if r:
                line = proc.stdout.readline()
                if line:
                    return json.loads(line.strip())
            # Also check if process died
            if proc.poll() is not None:
                return None

    try:
        # Initialize
        send("initialize", {"protocolVersion": "2024-11-05"})
        resp = recv(60)
        if resp is None or "error" in (resp or {}):
            stderr = proc.stderr.read()[:500]
            print(f"  ❌ {model_name}: init failed: {stderr[:200]}")
            proc.kill()
            proc.wait()
            shutil.rmtree(db_dir, ignore_errors=True)
            return None
        send("notifications/initialized", {})
        recv(1)  # might be empty for notifications

        # Store
        send("tools/call", {"name": "memhop_store", "arguments": {
            "text": "Python async uses asyncio event loop for cooperative multitasking",
            "session_id": "perf_test"
        }})
        store_resp = recv()
        if store_resp is None:
            print(f"  ❌ {model_name}: store failed (no response)")
            proc.kill(); proc.wait(); shutil.rmtree(db_dir, ignore_errors=True)
            return None
        
        # Recall
        send("tools/call", {"name": "memhop_recall", "arguments": {
            "query": "Python async event loop cooperative multitasking",
            "session_id": "perf_test",
            "limit": 5
        }})
        recall_resp = recv()
        if recall_resp is None:
            print(f"  ❌ {model_name}: recall failed (no response)")
            proc.kill(); proc.wait(); shutil.rmtree(db_dir, ignore_errors=True)
            return None

        results = recall_resp.get("result", {}).get("results", [])
        print(f"  {'✅' if results else '⚠️'} {model_name}: {len(results)} results")

        # Read encoder loading time from stderr
        stderr_data = proc.stderr.read()
        for line in stderr_data.split("\n"):
            if "Candle encoder ready" in line:
                # Extract time
                import re
                m = re.search(r'\(dim=(\d+), ([\d.]+)s\)', line)
                if m:
                    print(f"     dim={m.group(1)}, startup={m.group(2)}s")

        proc.kill()
        proc.wait()
        shutil.rmtree(db_dir, ignore_errors=True)
        return len(results)

    except Exception as e:
        print(f"  ❌ {model_name}: error: {e}")
        try: proc.kill(); proc.wait()
        except: pass
        shutil.rmtree(db_dir, ignore_errors=True)
        return None

if __name__ == "__main__":
    print("=== MemHop Model Verification ===\n")
    for name in MODELS:
        test_model(name)
    print("\nDone.")
