#!/usr/bin/env python3
"""MemHop Benchmark — one command to run them all.

Usage:
  python run_all.py                                    # everything
  python run_all.py --bench c_mteb                     # C-MTEB only
  python run_all.py --bench beir --subset 5000         # quick BEIR
  python run_all.py --bench latency --scales 1000,5000 # latency only
  python run_all.py --skip-competitors                 # MemHop only
"""

import os
import sys
import json
import argparse
import subprocess
import time
from pathlib import Path
from datetime import datetime

PROJECT_ROOT = Path(__file__).parent.parent

BENCH_BIN = PROJECT_ROOT / "target" / "release" / "quality_bench"
LATENCY_BIN = PROJECT_ROOT / "target" / "release" / "latency_bench"
MCP_SERVER_BIN = PROJECT_ROOT / "target" / "release" / "memhop-mcp-server"

REPORTS_DIR = Path(__file__).parent / "reports"
REPORTS_DIR.mkdir(parents=True, exist_ok=True)


def ensure_built():
    """Build Rust binaries if not present."""
    missing = []
    for name, path in [
        ("quality_bench", BENCH_BIN),
        ("latency_bench", LATENCY_BIN),
        ("memhop-mcp-server", MCP_SERVER_BIN),
    ]:
        if not path.exists():
            missing.append(name)

    if missing:
        print(f"Building: {', '.join(missing)}")
        subprocess.run(
            ["cargo", "build", "--release", "--features", "onnx"],
            cwd=PROJECT_ROOT,
            check=True,
        )
        print("Build complete.\n")


def run_c_mteb(args):
    """Run C-MTEB benchmarks."""
    print("\n" + "=" * 60)
    print("  C-MTEB Retrieval Benchmark")
    print("=" * 60)

    cmd = [
        sys.executable, str(Path(__file__).parent / "quality" / "run_c_mteb.py"),
        "--quality-bench-bin", str(BENCH_BIN),
        "--subset", str(args.subset),
        "--model", args.model,
        "--output", str(REPORTS_DIR / f"c_mteb_{datetime.now().strftime('%Y%m%d_%H%M%S')}.json"),
    ]
    if args.skip_competitors:
        cmd.append("--no-competitors")
    if args.task:
        cmd.extend(["--task", args.task])

    subprocess.run(cmd, check=True)


def run_beir(args):
    """Run BEIR benchmarks."""
    print("\n" + "=" * 60)
    print("  BEIR Zero-shot Retrieval Benchmark")
    print("=" * 60)

    cmd = [
        sys.executable, str(Path(__file__).parent / "quality" / "run_beir.py"),
        "--quality-bench-bin", str(BENCH_BIN),
        "--subset", str(args.subset),
        "--model", args.model,
        "--output", str(REPORTS_DIR / f"beir_{datetime.now().strftime('%Y%m%d_%H%M%S')}.json"),
    ]
    if args.skip_competitors:
        cmd.append("--no-competitors")
    if args.dataset:
        cmd.extend(["--dataset", args.dataset])

    subprocess.run(cmd, check=True)


def run_latency(args):
    """Run latency benchmarks."""
    print("\n" + "=" * 60)
    print("  Latency Benchmark")
    print("=" * 60)

    # Direct Rust binary (faster, no MCP overhead)
    cmd = [
        str(LATENCY_BIN),
        "--scales", args.scales or "1000,5000,10000,50000",
        "--queries", str(args.queries),
        "--output", str(REPORTS_DIR / f"latency_{datetime.now().strftime('%Y%m%d_%H%M%S')}.json"),
    ]
    subprocess.run(cmd, check=True)


def main():
    parser = argparse.ArgumentParser(
        description="MemHop Benchmark Suite",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python run_all.py                              # everything
  python run_all.py --bench c_mteb               # C-MTEB only
  python run_all.py --bench latency              # latency only
  python run_all.py --bench c_mteb,latency       # both
  python run_all.py --subset 5000 --skip-competitors  # quick memhop-only
        """,
    )
    parser.add_argument("--bench", type=str, default="c_mteb,beir,latency",
                        help="Comma-separated: c_mteb, beir, latency (default: all)")
    parser.add_argument("--subset", type=int, default=50000,
                        help="Document subset size per dataset")
    parser.add_argument("--task", type=str, help="Single C-MTEB task name")
    parser.add_argument("--dataset", type=str, help="Single BEIR dataset name")
    parser.add_argument("--scales", type=str, help="Latency scales (e.g., 1000,5000,10000)")
    parser.add_argument("--queries", type=int, default=50, help="Queries for latency test")
    parser.add_argument("--skip-competitors", action="store_true",
                        help="Skip FAISS/ChromaDB/Milvus comparison")
    parser.add_argument("--model", type=str, default="BAAI/bge-m3",
                        help="Embedding model for encoding")
    args = parser.parse_args()

    start_time = time.time()

    # Ensure Rust binaries are built
    ensure_built()

    benches = [b.strip() for b in args.bench.split(",")]

    print("╔══════════════════════════════════════════════════════════════╗")
    print("║   MemHop Benchmark Suite                                     ║")
    print("╚══════════════════════════════════════════════════════════════╝")
    print(f"  Benches: {benches}")
    print(f"  Subset: {args.subset} docs per dataset")
    print(f"  Competitors: {'skip' if args.skip_competitors else 'enabled'}")
    print(f"  Reports: {REPORTS_DIR}")
    print()

    if "c_mteb" in benches:
        run_c_mteb(args)

    if "beir" in benches:
        run_beir(args)

    if "latency" in benches:
        run_latency(args)

    elapsed = time.time() - start_time
    print(f"\n{'='*60}")
    print(f"  All benchmarks complete in {elapsed:.0f}s")
    print(f"  Reports: {REPORTS_DIR}")
    print(f"{'='*60}")


if __name__ == "__main__":
    main()
