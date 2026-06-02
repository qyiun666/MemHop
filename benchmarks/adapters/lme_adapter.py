"""LongMemEval-S data adapter.

Loads LME-S data from JSON and converts to the per-turn doc format
expected by MemHopMCPRunner.

Data format (one problem):
  {
    "question_id": "...",
    "question": "...",
    "answer": "...",
    "haystack_sessions": [[{"content": "...", ...}, ...], ...],
    "haystack_session_ids": ["sid1", "sid2", ...],
    "answer_session_ids": ["sid2"],
  }
"""

import json
from typing import Optional


def load_lme_dataset(
    path: str, subset: Optional[int] = None
) -> tuple[list[dict], list[dict], dict[str, dict[str, int]], dict[str, dict[str, int]]]:
    """Load LME-S dataset and return (docs, queries, turn_qrels, session_qrels).

    Args:
        path: Path to LME-S JSON file.
        subset: Number of problems to load (None = all).

    Returns:
        docs:  list of {"id", "text", "session_id", "turn_id", "turn_index"}
               Each turn is stored as a separate doc (per-turn).
        queries: list of {"id", "text"}
        turn_qrels:  {question_id: {turn_doc_id: 1}} — turn-level ground truth
        session_qrels: {question_id: {session_id: 1}} — session-level ground truth
    """
    with open(path) as f:
        problems = json.load(f)

    if subset:
        problems = problems[:subset]

    docs: list[dict] = []
    turn_qrels: dict[str, dict[str, int]] = {}
    session_qrels: dict[str, dict[str, int]] = {}
    queries: list[dict] = []

    for p in problems:
        ans_sids = set(p.get("answer_session_ids", []))
        if not ans_sids:
            continue
        haystack_sessions = p.get("haystack_sessions", [])
        haystack_ids = p.get("haystack_session_ids", [])
        qid = p.get("question_id", f"q_{len(queries)}")
        question = p.get("question", "")

        for si, turns in enumerate(haystack_sessions):
            if si >= len(haystack_ids):
                break
            sid = haystack_ids[si]
            for ti, turn in enumerate(turns):
                if not isinstance(turn, dict):
                    continue
                text = turn.get("content", "").strip()
                if not text:
                    continue
                doc_id = f"{sid}_t{ti}"
                docs.append(
                    {
                        "id": doc_id,
                        "text": text,
                        "session_id": sid,
                        "turn_id": doc_id,
                        "turn_index": ti,
                    }
                )
                # Turn-level qrels: every turn in answer session is relevant
                if sid in ans_sids:
                    turn_qrels.setdefault(qid, {})[doc_id] = 1

        queries.append({"id": qid, "text": question})
        # Session-level qrels (for associative mode evaluation)
        session_qrels[qid] = {sid: 1 for sid in ans_sids}

    return docs, queries, turn_qrels, session_qrels
