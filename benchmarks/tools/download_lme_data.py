"""LongMemEval-S download utility."""
import json, os, sys, urllib.request, zipfile, io

DATA_DIR = os.path.join(os.path.dirname(__file__), "../data/lme")
TARGET_FILE = os.path.join(DATA_DIR, "longmemeval_s_cleaned.json")

# Google Drive file ID for LongMemEval-S (from xiaowu0162/LongMemEval repo)
# The original paper links to Google Drive
GDRIVE_FILE_ID = "1t2e88Sj0YwPF0xPjSMcGqJJzqFjB9a_t"

SOURCES = [
    # 1. HuggingFace (requires login: hf auth login)
    {
        "name": "HuggingFace (xiaowu0162/longmemeval-cleaned)",
        "type": "huggingface",
        "repo": "xiaowu0162/longmemeval-cleaned",
        "repo_type": "dataset",
        "file": "longmemeval_s_cleaned.json",
    },
]


def download_from_huggingface(repo_id: str, filename: str, repo_type: str = "dataset") -> str:
    """Download from HuggingFace Hub."""
    try:
        from huggingface_hub import hf_hub_download
        return hf_hub_download(repo_id=repo_id, repo_type=repo_type, filename=filename, local_dir=DATA_DIR)
    except Exception as e:
        raise RuntimeError(f"HuggingFace download failed: {e}")


def download_from_url(url: str) -> str:
    """Download from a direct URL."""
    os.makedirs(DATA_DIR, exist_ok=True)
    print(f"  Downloading from {url[:80]}...")
    urllib.request.urlretrieve(url, TARGET_FILE)
    return TARGET_FILE


def ensure_dataset() -> str:
    """Download LongMemEval-S if not cached. Returns path to JSON file."""
    if os.path.exists(TARGET_FILE):
        size = os.path.getsize(TARGET_FILE)
        print(f"  ✅ Cached: {TARGET_FILE} ({size/1024/1024:.1f} MB)")
        return TARGET_FILE

    print("  Downloading LongMemEval-S dataset...")
    
    for source in SOURCES:
        try:
            if source.get("type") == "huggingface":
                path = download_from_huggingface(
                    source["repo"], source["file"],
                    source.get("repo_type", "dataset"),
                )
            else:
                path = download_from_url(source["url"])
            
            if os.path.exists(path):
                # Verify
                with open(path) as f:
                    data = json.load(f)
                print(f"  ✅ {len(data)} problems loaded from {source['name']}")
                return path
        except Exception as e:
            print(f"  ❌ {source['name']}: {e}")
            continue
    
    print("  ❌ All download sources failed")
    print("  Please manually download longmemeval_s_cleaned.json to:")
    print(f"    {TARGET_FILE}")
    print("  From: https://huggingface.co/datasets/weaviate/longmemeval-s-cleaned")
    return ""


if __name__ == "__main__":
    path = ensure_dataset()
    if path:
        with open(path) as f:
            data = json.load(f)
        print(f"\nDataset ready: {len(data)} problems")
        print(f"Sample: {json.dumps(data[0], indent=2)[:300]}")
