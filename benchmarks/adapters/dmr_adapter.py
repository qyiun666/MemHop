"""DMR (Deep Memory Retrieval) benchmark adapter.

Uses the Multi-Session Chat (MSC) dataset with 4-session conversations.
Sessions 1-3 are stored as memories; questions about their content
are generated via DeepSeek and evaluated with LLM-as-judge.

Based on the MemGPT DMR benchmark methodology:
  https://arxiv.org/abs/2310.08560
"""

import json
import os
from typing import Optional

from datasets import load_dataset

MSC_DATASET = "nayohan/multi_session_chat"

# MSC has 1/3/4 session conversations; use 4-session ones for DMR style
REQUIRED_SESSIONS = 4


def _get_conversations(subset: Optional[int] = None) -> list[dict]:
    """Load MSC and return sessions=4 conversations.

    Each returned dict:
      {"dialoug_id": int, "sessions": [
        {"session_id": int, "dialogue": [...], "speaker": [...]},
        ...
      ], "persona1": [...], "persona2": [...]}
    """
    # Ensure offline — data must be pre-cached via download_data.sh
    os.environ.setdefault("HF_DATASETS_OFFLINE", "1")
    try:
        ds = load_dataset(MSC_DATASET, split="train")
    except Exception as e:
        raise FileNotFoundError(
            f"DMR dataset (MSC) not cached. Please run: bash benchmarks/download_data.sh\n"
            f"  Error: {e}"
        )

    groups: dict[int, list] = {}
    for item in ds:
        did = item["dialoug_id"]
        groups.setdefault(did, []).append(item)

    result = []
    for did, sessions in groups.items():
        if len(sessions) != REQUIRED_SESSIONS:
            continue
        sessions.sort(key=lambda s: s["session_id"])
        result.append({
            "dialoug_id": did,
            "sessions": [
                {
                    "session_id": s["session_id"],
                    "dialogue": s["dialogue"],
                    "speaker": s["speaker"],
                }
                for s in sessions
            ],
            "persona1": sessions[0]["persona1"],
            "persona2": sessions[0]["persona2"],
        })

    if subset:
        result = result[:subset]

    return result


def _format_memory_text(conv: dict) -> str:
    """Format sessions 1-3 text for question generation."""
    parts = []
    for s in conv["sessions"]:
        if s["session_id"] >= REQUIRED_SESSIONS - 1:
            continue
        parts.append(f"\n--- Session {s['session_id'] + 1} ---")
        for speaker, text in zip(s["speaker"], s["dialogue"]):
            parts.append(f"[{speaker}] {text}")
    return "\n".join(parts)


def _make_docs(conv: dict) -> list[dict]:
    """Convert sessions 1-(N-1) into per-turn docs (memory store)."""
    docs = []
    did = conv["dialoug_id"]
    for s in conv["sessions"]:
        if s["session_id"] >= REQUIRED_SESSIONS - 1:
            continue
        sid = f"dmr_{did}_s{s['session_id']}"
        for ti, (speaker, text) in enumerate(zip(s["speaker"], s["dialogue"])):
            if not text.strip():
                continue
            doc_id = f"{sid}_t{ti}"
            docs.append({
                "id": doc_id,
                "text": f"[{speaker}] {text}",
                "session_id": sid,
                "turn_id": doc_id,
                "turn_index": ti,
            })
    return docs


def load_dmr_dataset(
    cache_dir: str,
    subset: Optional[int] = None,
    n_questions_per_conv: int = 3,
    force_regenerate: bool = False,
) -> tuple[list[dict], list[dict], list[str], list[dict]]:
    """Load DMR dataset: MSC 4-session conversations + generated questions.

    Args:
        cache_dir: Directory to cache questions JSON.
        subset: Number of conversations to use (None = all ~1001).
        n_questions_per_conv: Questions per conversation.
        force_regenerate: Regenerate questions even if cached.

    Returns:
        (all_docs, all_queries, all_answers, conv_metadata)
    """
    os.makedirs(cache_dir, exist_ok=True)
    cache_path = os.path.join(cache_dir, "dmr_questions.json")

    conversations = _get_conversations(subset)
    num_conv = len(conversations)

    all_docs: list[dict] = []
    all_queries: list[dict] = []
    all_answers: list[str] = []
    conv_metadata: list[dict] = []

    if os.path.exists(cache_path) and not force_regenerate:
        with open(cache_path) as f:
            cached = json.load(f)
        cached_map = {str(item["dialoug_id"]): item["questions"] for item in cached}

        for conv in conversations:
            did = str(conv["dialoug_id"])
            all_docs.extend(_make_docs(conv))
            conv_metadata.append({
                "dialoug_id": conv["dialoug_id"],
                "persona1": conv["persona1"],
                "persona2": conv["persona2"],
            })
            for qi, qa in enumerate(cached_map.get(did, [])):
                all_queries.append({"id": f"dmr_{did}_q{qi}", "text": qa["question"]})
                all_answers.append(qa["answer"])

        print(f"  {num_conv} convs, {len(all_queries)} questions (cached)")
        return all_docs, all_queries, all_answers, conv_metadata

    # First run: generate questions via DeepSeek
    from utils.llm_client import DeepSeekJudge

    judge = DeepSeekJudge()
    cached_data = []

    for ci, conv in enumerate(conversations):
        memory_text = _format_memory_text(conv)
        personas = conv.get("persona1", []) + conv.get("persona2", [])
        did = conv["dialoug_id"]
        print(f"  [{ci + 1}/{num_conv}] Gen Q&A conv {did}...")

        try:
            qa_pairs = judge.generate_dmr_questions(memory_text, personas, n_questions_per_conv)
        except Exception as e:
            print(f"    Failed: {e}")
            qa_pairs = []

        cached_data.append({"dialoug_id": did, "questions": qa_pairs})
        all_docs.extend(_make_docs(conv))
        conv_metadata.append({
            "dialoug_id": did,
            "persona1": conv["persona1"],
            "persona2": conv["persona2"],
        })
        for qi, qa in enumerate(qa_pairs):
            all_queries.append({"id": f"dmr_{did}_q{qi}", "text": qa["question"]})
            all_answers.append(qa["answer"])

        with open(cache_path, "w") as f:
            json.dump(cached_data, f, indent=2, ensure_ascii=False)

    print(f"  {num_conv} convs, {len(all_queries)} questions (generated)")
    return all_docs, all_queries, all_answers, conv_metadata
