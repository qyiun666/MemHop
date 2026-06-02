#!/bin/bash
# ================================================================
# MemHop Benchmark — 数据集预下载脚本
#
# 一次性下载所有 benchmark 数据集到 benchmarks/data/
# 下载完成后可离线运行 benchmark（无需网络）
#
# 用法:
#   bash benchmarks/download_data.sh
#
# 依赖:
#   - Python 3.10+ (pip install datasets mteb beir)
#   - curl
# ================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DATA_DIR="$SCRIPT_DIR/data"
PYTHON="${PYTHON:-python3}"

echo "╔══════════════════════════════════════════════════════════╗"
echo "║   MemHop Benchmark Data Downloader                       ║"
echo "╚══════════════════════════════════════════════════════════╝"

# ── 1. BEIR nfcorpus ────────────────────────────────────────
echo ""
echo "── [1/5] BEIR nfcorpus ──"
mkdir -p "$DATA_DIR/beir"
if [ -d "$DATA_DIR/beir/nfcorpus" ]; then
    echo "  ✅ 已存在，跳过"
else
    echo "  下载中..."
    cd "$SCRIPT_DIR"
    $PYTHON -c "
from beir import util
url = 'https://public.ukp.informatik.tu-darmstadt.de/thakur/BEIR/datasets/nfcorpus.zip'
util.download_and_unzip(url, '$DATA_DIR/beir')
"
    echo "  ✅ nfcorpus 下载完成"
fi

# ── 2. C-MTEB retrieval tasks ───────────────────────────────
echo ""
echo "── [2/5] C-MTEB retrieval tasks ──"
$PYTHON -c "
import os
os.environ['HF_DATASETS_OFFLINE'] = '0'
from mteb import get_task
tasks = ['T2Retrieval', 'MMarcoRetrieval', 'DuRetrieval', 'CovidRetrieval',
         'CmedqaRetrieval', 'EcomRetrieval', 'MedicalRetrieval', 'VideoRetrieval']
for name in tasks:
    print(f'  Loading {name}...')
    t = get_task(name)
    t.load_data()
    print(f'    ✅')
print('  ✅ C-MTEB 全部下载完成')
"

# ── 3. LoCoMo ───────────────────────────────────────────────
echo ""
echo "── [3/5] LoCoMo ──"
mkdir -p "$DATA_DIR/locomo"
LOCOMO_PATH="$DATA_DIR/locomo/locomo10.json"
if [ -f "$LOCOMO_PATH" ]; then
    echo "  ✅ 已存在，跳过"
else
    echo "  下载中..."
    curl -# -o "$LOCOMO_PATH" \
        "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"
    echo "  ✅ LoCoMo 下载完成"
fi

# ── 4. DMR (MSC dataset) ────────────────────────────────────
echo ""
echo "── [4/5] DMR (MSC dataset) ──"
$PYTHON -c "
from datasets import load_dataset
print('  Loading nayohan/multi_session_chat...')
ds = load_dataset('nayohan/multi_session_chat', split='train')
print(f'  ✅ {len(ds)} 条记录缓存完成')
"

# ── 5. LME-S (LongMemEval-S) ───────────────────────────────
echo ""
echo "── [5/5] LongMemEval-S ──"
mkdir -p "$DATA_DIR/lme"
LME_PATH="$DATA_DIR/lme/longmemeval_s_cleaned.json"
if [ -f "$LME_PATH" ]; then
    echo "  ✅ 已存在，跳过"
else
    echo "  ⚠️  LME-S 数据需要手动放置到:"
    echo "     $LME_PATH"
    echo "  请从 LongMemEval 仓库下载并放置到该路径。"
fi

echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║   下载完成！现在可以离线运行 benchmark                    ║"
echo "║   HF_DATASETS_OFFLINE=1 确保不联网                     ║"
echo "╚══════════════════════════════════════════════════════════╝"
