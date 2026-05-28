#!/usr/bin/env python3
"""Run latency benchmarks via MCP client.

Usage:
  python run_latency.py --server ../target/release/memhop-mcp-server
"""

import os
import sys
import json
import time
import argparse
import subprocess
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent))

from mcp_client import MemHopMCPClient


def run_latency_via_mcp(server_path: str, scales: list[int], queries: int = 50):
    """Run latency benchmarks via MCP client — measures end-to-end including MCP overhead."""

    import tempfile
    import shutil

    results = []

    for scale in scales:
        print(f"\n--- Scale: {scale} ---")

        db_dir = tempfile.mkdtemp(prefix=f"memhop_lat_{scale}_")
        db_path = os.path.join(db_dir, "bench.db")

        client = MemHopMCPClient(server_path, db_path)
        client.start_reader()
        time.sleep(0.5)

        # Store
        store_lats = []
        for i in range(scale):
            text = f"Memo #{i}: Alice refactored the auth handler. build-{i % 97}-r{i % 31}"
            t0 = time.time()
            client.store(text, session_id="bench")
            store_lats.append((time.time() - t0) * 1_000_000)  # µs

        store_lats.sort()
        store_p50 = store_lats[len(store_lats) * 50 // 100]
        store_p95 = store_lats[len(store_lats) * 95 // 100]
        store_p99 = store_lats[len(store_lats) * 99 // 100]
        store_ops = scale / (sum(store_lats) / 1_000_000) if sum(store_lats) > 0 else 0

        print(f"  Store:  P50={store_p50:.0f}µs  P95={store_p95:.0f}µs  P99={store_p99:.0f}µs  {store_ops:.0f} ops/s")

        # Recall
        recall_lats = []
        for i in range(queries):
            q = f"what did Alice change in the auth handler build-{(i*7)%97}"
            t0 = time.time()
            client.recall(q, session_id="bench", limit=10)
            recall_lats.append((time.time() - t0) * 1_000_000)

        recall_lats.sort()
        recall_p50 = recall_lats[len(recall_lats) * 50 // 100]
        recall_p95 = recall_lats[len(recall_lats) * 95 // 100]
        recall_p99 = recall_lats[len(recall_lats) * 99 // 100]
        recall_ops = queries / (sum(recall_lats) / 1_000_000) if sum(recall_lats) > 0 else 0

        print(f"  Recall: P50={recall_p50:.0f}µs  P95={recall_p95:.0f}µs  P99={recall_p99:.0f}µs  {recall_ops:.0f} ops/s")

        # Stats
        stats = client.stats()
        print(f"  Memories: {stats.get('total_memories', 0)}")

        # Disk size
        disk_size = 0
        for root, dirs, files in os.walk(db_dir):
            for f in files:
                fp = os.path.join(root, f)
                disk_size += os.path.getsize(fp)
        disk_mb = disk_size / (1024 * 1024)

        client.close()
        shutil.rmtree(db_dir, ignore_errors=True)

        results.append({
            "scale": scale,
            "store_p50_us": store_p50,
            "store_p95_us": store_p95,
            "store_p99_us": store_p99,
            "store_ops_per_sec": store_ops,
            "recall_p50_us": recall_p50,
            "recall_p95_us": recall_p95,
            "recall_p99_us": recall_p99,
            "recall_ops_per_sec": recall_ops,
            "disk_size_mb": disk_mb,
        })

    return results


def main():
    parser = argparse.ArgumentParser(description="MemHop latency benchmark via MCP")
    parser.add_argument("--server", type=str, required=True, help="Path to memhop-mcp-server binary")
    parser.add_argument("--scales", type=str, default="1000,5000,10000", help="Comma-separated scales")
    parser.add_argument("--queries", type=int, default=50)
    parser.add_argument("--output", type=str, default=None)
    args = parser.parse_args()

    scales = [int(s) for s in args.scales.split(",")]

    print(f"=== MemHop Latency Benchmark (via MCP) ===")
    print(f"Scales: {scales}")
    print(f"Queries per scale: {args.queries}")
    print(f"Server: {args.server}")

    results = run_latency_via_mcp(args.server, scales, args.queries)

    print(f"\n{'='*70}")
    print("Summary")
    print(f"{'='*70}")
    print(f"  {'Scale':>8}  {'Store P95':>12}  {'Recall P95':>12}  {'Disk':>8}")
    for r in results:
        print(f"  {r['scale']:>6}K  {r['store_p95_us']:>10.0f}µs  {r['recall_p95_us']:>10.0f}µs  {r['disk_size_mb']:>6.1f}MB")

    if args.output:
        with open(args.output, "w") as f:
            json.dump({"results": results}, f, indent=2)
        print(f"\nSaved: {args.output}")


if __name__ == "__main__":
    main()
