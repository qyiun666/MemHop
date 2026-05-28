#!/usr/bin/env python3
"""Download BGE-Reranker-v2-m3 ONNX model for MemHop Cross-Encoder.

Usage:
  python3 scripts/download_reranker.py

Output: models/bge-reranker-v2-m3/model.onnx + tokenizer.json
"""

import os
import sys
import subprocess
from pathlib import Path

MODEL_NAME = "BAAI/bge-reranker-v2-m3"
OUTPUT_DIR = Path("models/bge-reranker-v2-m3")


def check_deps():
    try:
        import torch  # noqa
        import transformers  # noqa
        import optimum  # noqa
        return True
    except ImportError:
        return False


def install_deps():
    reqs = ["torch", "transformers", "optimum[onnx]"]
    print("Installing:", " ".join(reqs))
    subprocess.check_call([sys.executable, "-m", "pip", "install", "-q"] + reqs)


def export_model():
    print(f"Exporting {MODEL_NAME} to ONNX...")
    subprocess.check_call([
        sys.executable, "-m", "optimum.cli", "export", "onnx",
        "--model", MODEL_NAME, str(OUTPUT_DIR),
    ])


def verify():
    required = ["model.onnx", "tokenizer.json"]
    missing = [f for f in required if not (OUTPUT_DIR / f).exists()]
    if missing:
        print(f"ERROR: missing {missing}")
        sys.exit(1)
    mb = (OUTPUT_DIR / "model.onnx").stat().st_size / 1024 / 1024
    print(f"  model.onnx: {mb:.1f} MB")
    print("OK")


def main():
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    if (OUTPUT_DIR / "model.onnx").exists():
        print("Already exists:", OUTPUT_DIR)
        verify()
        return
    if not check_deps():
        install_deps()
    export_model()
    verify()
    print(f"Done: {OUTPUT_DIR}")


if __name__ == "__main__":
    main()
