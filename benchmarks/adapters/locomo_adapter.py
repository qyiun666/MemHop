"""LoCoMo benchmark data adapter.

Loads LoCoMo data from local cache. Data must be pre-downloaded
via benchmarks/download_data.sh.

Data source: https://github.com/snap-research/locomo
"""

import json
import os
import re
from typing import Optional

LOCOMO_FILENAME = "locomo10.json"


def _get_session_keys(conv: dict) -> list[str]:
    """Extract session_N keys from the conversation dict, sorted by N."""
    keys = []
    for k in conv:
        if re.match(r"^session_\d+$", k) and not k.endswith("_date_time"):
            keys.append(k)
    keys.sort(key=lambda k: int(k.split("_")[1]))
    return keys


def load_locomo_dataset(
    data_dir: str,
    subset: Optional[int] = None,
) -> tuple[list[dict], list[dict], list[str]]:
    """Load LoCoMo dataset and return (docs, queries, answer_texts).

    Each turn across all sessions is stored as an individual doc (per-turn).
    Each QA pair becomes a separate query.
    Answers are returned for text-containment-based F1 evaluation.

    Args:
        data_dir: Directory to cache the downloaded JSON.
        subset: Number of dialogues to load (None = all, default 10).

    Returns:
        docs:  list of {"id", "text", "session_id", "turn_id", "turn_index"}
        queries: list of {"id", "text"}
        answers: list of str
    """
    path = os.path.join(data_dir, LOCOMO_FILENAME)
    if not os.path.exists(path):
        raise FileNotFoundError(
            f"LoCoMo data not found at {path}.\n"
            f"  Please run: bash benchmarks/download_data.sh"
        )

    with open(path) as f:
        dialogues = json.load(f)

    if subset:
        dialogues = dialogues[:subset]

    docs: list[dict] = []
    queries: list[dict] = []
    answers: list[str] = []

    for di, dialogue in enumerate(dialogues):
        conv = dialogue.get("conversation", {})
        qa_list = dialogue.get("qa", [])
        conv_id = f"locomo_{di}"

        # Collect all sessions and compute a linear turn index
        session_keys = _get_session_keys(conv)
        linear_ti = 0
        for si, sk in enumerate(session_keys):
            turns = conv.get(sk, [])
            if not isinstance(turns, list):
                continue
            sid = f"{conv_id}_s{si}"
            date_str = conv.get(f"{sk}_date_time", "")
            date_prefix = f"[{date_str}] " if date_str else ""
            for turn in turns:
                if not isinstance(turn, dict):
                    continue
                text = turn.get("text", "").strip()
                if not text:
                    continue
                speaker = turn.get("speaker", "")
                speaker_prefix = f"[{speaker}] " if speaker else ""
                doc_id = f"{sid}_t{linear_ti}"
                docs.append({
                    "id": doc_id,
                    "text": date_prefix + speaker_prefix + text,
                    "session_id": sid,
                    "turn_id": doc_id,
                    "turn_index": linear_ti,
                })
                linear_ti += 1

        # Each QA pair becomes a query
        for qi, qa in enumerate(qa_list):
            qid = f"{conv_id}_q{qi}"
            question = qa.get("question", "")
            answer = qa.get("answer", "")
            if not question:
                continue
            queries.append({"id": qid, "text": question})
            answers.append(str(answer) if answer is not None else "")

    return docs, queries, answers
