"""Benchmark configuration — single source of truth for paths and defaults."""

import os

# ── version ────────────────────────────────────────────────
MEMHOP_VERSION = "0.13.1"

# ── paths ──────────────────────────────────────────────────
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
REPORT_DIR = os.path.join(SCRIPT_DIR, "reports")

# MCP server binary (override with $MEMHOP_MCP_BIN)
MCP_BIN = os.environ.get(
    "MEMHOP_MCP_BIN",
    os.path.join(os.path.dirname(SCRIPT_DIR), "target/release/memhop-mcp-server"),
)

# LongMemEval-S data path (override with $LME_DATA_PATH)
LME_DATA = os.environ.get(
    "LME_DATA_PATH",
    os.path.join(SCRIPT_DIR, "data", "lme", "longmemeval_s_cleaned.json"),
)

# ── model directory mapping ────────────────────────────────
# encoder name → subdirectory name under models/
MODEL_MAP = {
    "bge-m3": "bge-m3",
    "bge-small-en": "bge-small-en-v1.5",
    "bge-small-zh": "bge-small-zh-v1.5",
    "bge-base-en": "bge-base-en-v1.5",
    "bge-base-zh": "bge-base-zh-v1.5",
}
MODEL_DIR = os.path.join(os.path.dirname(SCRIPT_DIR), "models")

# ── competitor data ────────────────────────────────────────
COMPETITOR_DATA = os.path.join(SCRIPT_DIR, "competitors_published.json")

# ── dataset defaults ───────────────────────────────────────
# All datasets, in recommended run order (conversational first, document last)
ALL_DATASETS = ["locomo", "lme-s", "dmr", "nfcorpus", "c-mteb"]
DEFAULT_SUBSET = None       # None = full dataset
DEFAULT_ENCODER = "bge-m3"  # default encoder
