#!/usr/bin/env bash
#
# Download and convert benchmark datasets for MemHop
#
# Usage: ./download_datasets.sh
#
# This script:
# 1. Clones LOCOMO repository to /tmp/locomo_clone
# 2. Extracts conversation data and questions
# 3. Converts to MemHop-consumable JSON format
# 4. Generates smoke subset (first 2 conversations + 20 questions)
# 5. Cleans up temporary files
# 6. Output files go to benches/fixtures/locomo/
#

set -euo pipefail

# ============================================================================
# Configuration
# ============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/locomo"
TMP_DIR="/tmp/locomo_clone_$$"
LOCOMO_REPO="https://github.com/snap-research/locomo.git"
SMOKE_SESSIONS=2
SMOKE_QUESTIONS=20

# ============================================================================
# Helper functions
# ============================================================================

log() {
    echo "[INFO] $*"
}

error() {
    echo "[ERROR] $*" >&2
    exit 1
}

cleanup() {
    if [[ -d "$TMP_DIR" ]]; then
        log "Cleaning up temporary directory: $TMP_DIR"
        rm -rf "$TMP_DIR"
    fi
}

# ============================================================================
# Main
# ============================================================================

main() {
    log "Starting dataset download and conversion"
    
    # Ensure output directory exists
    mkdir -p "$OUTPUT_DIR"
    
    # Set up cleanup trap
    trap cleanup EXIT
    
    # Step 1: Clone LOCOMO repository
    log "Cloning LOCOMO repository..."
    if [[ -d "$TMP_DIR" ]]; then
        rm -rf "$TMP_DIR"
    fi
    
    git clone --depth 1 "$LOCOMO_REPO" "$TMP_DIR" || {
        error "Failed to clone LOCOMO repository. Check network connection."
    }
    
    # Step 2: Find conversation and question files
    log "Locating data files..."
    
    # LOCOMO typically stores data in data/ directory
    local data_dir="$TMP_DIR/data"
    if [[ ! -d "$data_dir" ]]; then
        # Try alternative locations
        data_dir="$TMP_DIR/locomo"
        if [[ ! -d "$data_dir" ]]; then
            data_dir="$TMP_DIR"
        fi
    fi
    
    # Look for JSON files containing conversations
    local conv_files
    conv_files=$(find "$data_dir" -name "*.json" -type f | head -20)
    
    if [[ -z "$conv_files" ]]; then
        error "No JSON data files found in $data_dir"
    fi
    
    log "Found data files: $(echo "$conv_files" | wc -l | tr -d ' ')"
    
    # Step 3: Convert to MemHop format
    # LOCOMO format varies, we'll create a Python script for robust parsing
    log "Converting data to MemHop format..."
    
    cat > "${TMP_DIR}/convert.py" << 'PYTHON_SCRIPT'
#!/usr/bin/env python3
"""Convert LOCOMO dataset to MemHop-consumable JSON format."""

import json
import os
import sys
from pathlib import Path
from datetime import datetime

def convert_locomo_to_memhop(input_dir, output_dir, max_sessions=None, max_questions=None):
    """Convert LOCOMO data to MemHop format."""
    
    # Find all JSON files
    json_files = list(Path(input_dir).rglob("*.json"))
    
    sessions = []
    questions = []
    
    for jf in json_files:
        try:
            with open(jf, 'r', encoding='utf-8') as f:
                data = json.load(f)
        except (json.JSONDecodeError, UnicodeDecodeError):
            continue
        
        # Handle different LOCOMO formats
        # Format 1: List of conversation objects
        if isinstance(data, list):
            for idx, item in enumerate(data):
                if max_sessions and len(sessions) >= max_sessions:
                    break
                
                session = convert_conversation(item, f"locomo_{jf.stem}_{idx}")
                if session:
                    sessions.append(session)
        
        # Format 2: Single conversation object
        elif isinstance(data, dict):
            # Check if it has conversation data
            if "conversation" in data or "dialogue" in data or "turns" in data:
                session = convert_conversation(data, f"locomo_{jf.stem}")
                if session:
                    sessions.append(session)
            
            # Check if it has questions
            if "questions" in data or "qa_pairs" in data:
                qs = extract_questions(data)
                questions.extend(qs)
    
    # If no questions extracted, generate from sessions
    if not questions and sessions:
        questions = generate_questions_from_sessions(sessions[:max_sessions or len(sessions)])
    
    # Apply limits
    if max_sessions:
        sessions = sessions[:max_sessions]
    if max_questions:
        questions = questions[:max_questions]
    
    # Create output
    output = {
        "metadata": {
            "source": "locomo",
            "version": "1.0",
            "description": "LOCOMO benchmark dataset converted for MemHop",
            "converted_at": datetime.utcnow().isoformat() + "Z",
            "session_count": len(sessions),
            "question_count": len(questions)
        },
        "sessions": sessions,
        "questions": questions
    }
    
    # Write output
    os.makedirs(output_dir, exist_ok=True)
    output_file = os.path.join(output_dir, "locomo_full.json")
    with open(output_file, 'w', encoding='utf-8') as f:
        json.dump(output, f, indent=2, ensure_ascii=False)
    
    # Write smoke subset
    smoke_sessions = sessions[:2]
    smoke_questions = questions[:20]
    
    smoke_output = {
        "metadata": {
            "source": "locomo_smoke",
            "version": "1.0",
            "description": "Small smoke test subset of LOCOMO for CI validation",
            "converted_at": datetime.utcnow().isoformat() + "Z",
            "session_count": len(smoke_sessions),
            "question_count": len(smoke_questions)
        },
        "sessions": smoke_sessions,
        "questions": smoke_questions
    }
    
    smoke_file = os.path.join(output_dir, "locomo_smoke.json")
    with open(smoke_file, 'w', encoding='utf-8') as f:
        json.dump(smoke_output, f, indent=2, ensure_ascii=False)
    
    print(f"Converted {len(sessions)} sessions and {len(questions)} questions")
    print(f"Output: {output_file}")
    print(f"Smoke: {smoke_file}")

def convert_conversation(data, session_id):
    """Convert a single conversation to MemHop session format."""
    
    turns = []
    
    # Extract turns from various formats
    turn_list = data.get("conversation", data.get("dialogue", data.get("turns", [])))
    
    if isinstance(turn_list, list):
        for idx, turn in enumerate(turn_list):
            if isinstance(turn, dict):
                text = turn.get("text", turn.get("content", turn.get("utterance", "")))
                speaker = turn.get("speaker", turn.get("role", "unknown"))
                timestamp = turn.get("timestamp", 1700000000 + idx * 60)
            elif isinstance(turn, str):
                text = turn
                speaker = "unknown"
                timestamp = 1700000000 + idx * 60
            else:
                continue
            
            if text:
                turns.append({
                    "text": str(text),
                    "timestamp": timestamp,
                    "speaker": speaker
                })
    
    if not turns:
        return None
    
    return {
        "id": session_id,
        "turns": turns
    }

def extract_questions(data):
    """Extract questions from LOCOMO format."""
    
    questions = []
    qa_list = data.get("questions", data.get("qa_pairs", []))
    
    if isinstance(qa_list, list):
        for idx, qa in enumerate(qa_list):
            if isinstance(qa, dict):
                q = qa.get("question", qa.get("query", ""))
                a = qa.get("answer", qa.get("response", ""))
                cat = qa.get("category", qa.get("type", "single_hop"))
                
                if q and a:
                    questions.append({
                        "id": f"q_{idx}",
                        "question": str(q),
                        "answer": str(a),
                        "category": normalize_category(cat),
                        "session_refs": qa.get("session_refs", [])
                    })
    
    return questions

def normalize_category(cat):
    """Normalize category names."""
    cat_lower = str(cat).lower().replace("-", "_").replace(" ", "_")
    
    if "single" in cat_lower or "factoid" in cat_lower:
        return "single_hop"
    elif "multi" in cat_lower or "hop" in cat_lower:
        return "multi_hop"
    elif "open" in cat_lower or "domain" in cat_lower:
        return "open_domain"
    elif "temporal" in cat_lower or "time" in cat_lower:
        return "temporal"
    else:
        return "single_hop"

def generate_questions_from_sessions(sessions):
    """Generate simple questions from session data (fallback)."""
    questions = []
    
    for session in sessions[:10]:  # Limit to first 10 sessions
        turns = session.get("turns", [])
        if not turns:
            continue
        
        # Generate a question from the first turn
        first_turn = turns[0].get("text", "")
        if len(first_turn) > 20:
            # Create a simple question
            questions.append({
                "id": f"gen_{session['id']}_q1",
                "question": f"What was discussed in {session['id']}?",
                "answer": first_turn[:200],
                "category": "single_hop",
                "session_refs": [session["id"]]
            })
    
    return questions

if __name__ == "__main__":
    input_dir = sys.argv[1] if len(sys.argv) > 1 else "."
    output_dir = sys.argv[2] if len(sys.argv) > 2 else "./output"
    max_sessions = int(sys.argv[3]) if len(sys.argv) > 3 else None
    max_questions = int(sys.argv[4]) if len(sys.argv) > 4 else None
    
    convert_locomo_to_memhop(input_dir, output_dir, max_sessions, max_questions)
PYTHON_SCRIPT
    
    # Run conversion
    python3 "${TMP_DIR}/convert.py" "$data_dir" "$OUTPUT_DIR" "$SMOKE_SESSIONS" "$SMOKE_QUESTIONS" || {
        error "Python conversion failed. Ensure python3 is installed."
    }
    
    # Step 4: Verify output
    if [[ ! -f "$OUTPUT_DIR/locomo_full.json" ]] || [[ ! -f "$OUTPUT_DIR/locomo_smoke.json" ]]; then
        error "Conversion produced no output files"
    fi
    
    local session_count
    session_count=$(python3 -c "import json; d=json.load(open('$OUTPUT_DIR/locomo_full.json')); print(len(d['sessions']))")
    
    local question_count
    question_count=$(python3 -c "import json; d=json.load(open('$OUTPUT_DIR/locomo_full.json')); print(len(d['questions']))")
    
    log "Conversion complete: $session_count sessions, $question_count questions"
    log "Full dataset: $OUTPUT_DIR/locomo_full.json"
    log "Smoke subset: $OUTPUT_DIR/locomo_smoke.json"
    
    log "Done!"
}

# Run main
main "$@"
