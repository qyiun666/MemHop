#!/usr/bin/env python3
"""
Convert LOCOMO dataset to MemHop benchmark format.

LOCOMO format:
- Array of scenarios, each containing:
  - qa: array of {question, answer, evidence, category}
  - conversation: dict with speaker_a, speaker_b, and session_N keys
  - sample_id: scenario identifier

Category mapping (LOCOMO -> MemHop):
- 1 -> single_hop (simple factual questions)
- 2 -> temporal (time-related questions)
- 3 -> multi_hop (requires reasoning across multiple pieces of information)
- 4 -> open_domain (open-ended questions, if present)

Evidence format: "D{session}:{turn}" where session is 1-indexed and turn is 1-indexed
"""

import json
import sys
import os
from datetime import datetime, timedelta
import random
import re

def parse_evidence(evidence_list):
    """Parse evidence strings like 'D1:3' into session references."""
    session_refs = set()
    for ev in evidence_list:
        # Format: D{session_number}:{turn_number}
        match = re.match(r'D(\d+):(\d+)', ev)
        if match:
            session_num = int(match.group(1))
            session_refs.add(f"session_{session_num}")
    return sorted(list(session_refs))

def map_category(category_num):
    """Map LOCOMO category number to MemHop category string."""
    # Based on LOCOMO paper and data analysis:
    # 1: Single-hop factual questions
    # 2: Temporal/time-related questions
    # 3: Multi-hop reasoning questions
    # 4: Open-domain questions
    # 5: Adversarial questions (with adversarial_answer)
    category_map = {
        1: "single_hop",
        2: "temporal",
        3: "multi_hop",
        4: "open_domain",
        5: "adversarial"  # Will be mapped to open_domain in output
    }
    return category_map.get(category_num, "single_hop")

def generate_timestamp(base_ts, turn_index, session_index):
    """Generate a realistic timestamp for a conversation turn."""
    # Each turn is roughly 30-90 seconds apart
    turn_offset = turn_index * random.randint(30, 90)
    # Each session is roughly 1-7 days apart
    session_offset = session_index * random.randint(86400, 604800)
    return base_ts + session_offset + turn_offset

def convert_locomo_to_memhop(input_file, output_file):
    """Convert LOCOMO dataset to MemHop benchmark format."""
    print(f"Loading LOCOMO data from: {input_file}")
    
    with open(input_file, 'r', encoding='utf-8') as f:
        locomo_data = json.load(f)
    
    sessions = []
    questions = []
    
    # Base timestamp: 2023-01-01 00:00:00 UTC
    base_timestamp = 1672531200
    
    total_scenarios = len(locomo_data)
    print(f"Processing {total_scenarios} scenarios...")
    
    for scenario_idx, scenario in enumerate(locomo_data):
        conversation = scenario.get('conversation', {})
        qa_list = scenario.get('qa', [])
        sample_id = scenario.get('sample_id', f'scenario_{scenario_idx}')
        
        # Get speaker names
        speaker_a = conversation.get('speaker_a', 'Speaker A')
        speaker_b = conversation.get('speaker_b', 'Speaker B')
        
        # Process each session in the conversation
        # Find all session keys (session_1, session_2, etc.)
        session_keys = sorted([k for k in conversation.keys() if k.startswith('session_') and not k.endswith('_date_time')])
        
        for session_idx, session_key in enumerate(session_keys):
            session_data = conversation[session_key]
            
            if not isinstance(session_data, list):
                continue
            
            # Create session ID: scenario_{scenario_idx}_session_{session_num}
            session_num = session_key.replace('session_', '')
            session_id = f"scenario_{scenario_idx}_session_{session_num}"
            
            # Convert turns
            turns = []
            for turn_idx, turn in enumerate(session_data):
                speaker = turn.get('speaker', speaker_a)
                text = turn.get('text', '')
                dia_id = turn.get('dia_id', '')
                
                # Generate timestamp
                timestamp = generate_timestamp(base_timestamp, turn_idx, session_idx)
                
                turns.append({
                    "text": text,
                    "timestamp": timestamp,
                    "speaker": speaker
                })
            
            if turns:
                sessions.append({
                    "id": session_id,
                    "turns": turns
                })
        
        # Process questions
        for q_idx, qa in enumerate(qa_list):
            question_text = qa.get('question', '')
            evidence = qa.get('evidence', [])
            category_num = qa.get('category', 1)
            
            # Handle answer field - category 5 uses adversarial_answer
            if 'answer' in qa:
                answer = qa['answer']
            elif 'adversarial_answer' in qa:
                answer = qa['adversarial_answer']
            else:
                answer = ""
            
            # Parse evidence to get session references
            session_refs = parse_evidence(evidence)
            
            # Map to full session IDs
            full_session_refs = []
            for ref in session_refs:
                session_num = ref.replace('session_', '')
                full_ref = f"scenario_{scenario_idx}_session_{session_num}"
                full_session_refs.append(full_ref)
            
            # Map category
            category = map_category(category_num)
            # Map adversarial to open_domain for MemHop compatibility
            if category == "adversarial":
                category = "open_domain"
            
            # Create question ID
            question_id = f"q_{scenario_idx}_{q_idx}"
            
            questions.append({
                "id": question_id,
                "question": question_text,
                "answer": str(answer),
                "category": category,
                "session_refs": full_session_refs
            })
    
    # Create output structure
    output = {
        "metadata": {
            "source": "locomo",
            "version": "1.0",
            "description": "Full LOCOMO dataset converted to MemHop benchmark format",
            "created_at": datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ"),
            "session_count": len(sessions),
            "question_count": len(questions),
            "scenario_count": total_scenarios
        },
        "sessions": sessions,
        "questions": questions
    }
    
    # Write output
    print(f"Writing {len(sessions)} sessions and {len(questions)} questions to: {output_file}")
    with open(output_file, 'w', encoding='utf-8') as f:
        json.dump(output, f, indent=2, ensure_ascii=False)
    
    print("Conversion complete!")
    return output

def main():
    if len(sys.argv) < 3:
        print("Usage: python3 convert_locomo.py <input_file> <output_file>")
        print("Example: python3 convert_locomo.py /tmp/locomo/data/locomo10.json benches/fixtures/locomo_full.json")
        sys.exit(1)
    
    input_file = sys.argv[1]
    output_file = sys.argv[2]
    
    # Ensure output directory exists
    os.makedirs(os.path.dirname(output_file), exist_ok=True)
    
    # Convert
    result = convert_locomo_to_memhop(input_file, output_file)
    
    # Print summary
    print("\nSummary:")
    print(f"  Total scenarios: {result['metadata']['scenario_count']}")
    print(f"  Total sessions: {result['metadata']['session_count']}")
    print(f"  Total questions: {result['metadata']['question_count']}")
    
    # Category distribution
    categories = {}
    for q in result['questions']:
        cat = q['category']
        categories[cat] = categories.get(cat, 0) + 1
    
    print("\nQuestion categories:")
    for cat, count in sorted(categories.items()):
        print(f"  {cat}: {count}")

if __name__ == "__main__":
    main()
