# Benchmark Datasets

This directory contains benchmark datasets for evaluating MemHop's memory retrieval capabilities.

## Datasets

### LOCOMO (Long Conversation Memory)

**Source**: [snap-research/locomo](https://github.com/snap-research/locomo)

LOCOMO is a benchmark for long-term conversational memory evaluation. It contains:
- 10 long conversations (11k-23k tokens each)
- ~1,986 questions across 4 categories:
  - **single-hop**: Questions answerable from a single conversation turn
  - **multi-hop**: Questions requiring information from multiple turns
  - **open-domain**: Questions requiring external knowledge
  - **temporal/conversational**: Questions about time or conversation flow

**License**: Apache-2.0

### LongMemEval

**Source**: [xiaowu0162/LongMemEval](https://github.com/xiaowu0162/LongMemEval)

LongMemEval is a benchmark for evaluating long-term memory in LLMs. It contains:
- 500 questions across 6 capability dimensions:
  - **information_extraction**: Extract specific facts from memory
  - **temporal**: Reason about temporal relationships
  - **multi_session**: Combine information across sessions
  - **knowledge_update**: Handle updated information
  - **abstention**: Know when information is not available
  - **reasoning**: Perform complex reasoning over memories

**License**: MIT

## Download & Conversion

### Quick Start (LOCOMO)

```bash
# Download and convert LOCOMO dataset
./download_datasets.sh

# This will:
# 1. Clone LOCOMO repository to /tmp/locomo_clone
# 2. Extract conversation data and questions
# 3. Convert to MemHop-consumable JSON format
# 4. Generate smoke subset (first 2 conversations + 20 questions)
# 5. Output to benches/fixtures/locomo/
```

### Smoke Subsets (CI Quick Validation)

For CI/CD pipelines, use the smoke test datasets that don't require external downloads:

- `locomo_smoke.json`: 2 synthetic conversations + 10 questions
- `longmemeval_smoke.json`: 5 questions covering different categories

These files are self-contained and can be used directly in tests.

## Data Format

### Conversation Format (for `batch_store`)

```json
{
  "sessions": [
    {
      "id": "session_id",
      "turns": [
        {
          "text": "Conversation turn text",
          "timestamp": 1700000000,
          "speaker": "user/assistant"
        }
      ]
    }
  ]
}
```

### Question Format (for search evaluation)

```json
{
  "questions": [
    {
      "id": "question_id",
      "question": "Question text",
      "answer": "Ground truth answer",
      "category": "single_hop|multi_hop|open_domain|temporal",
      "session_refs": ["session_id_1", "session_id_2"]
    }
  ]
}
```

### MemHop StoreItem Format

Each conversation turn maps to a `batch_store` item:

```json
{
  "text": "Conversation turn text",
  "topic_label": "session_topic",
  "domain_id": "conversation",
  "importance": 0.7,
  "valence": 0.0,
  "arousal": 0.0,
  "source": {
    "source_type": "UserInput",
    "source_id": "session_id",
    "timestamp": 1700000000000
  },
  "is_structural": false,
  "source_ref": null
}
```

## Usage in Benchmarks

```rust
// Load smoke dataset
let data = include_str!("../fixtures/locomo_smoke.json");
let dataset: serde_json::Value = serde_json::from_str(data)?;

// Store conversations
for session in dataset["sessions"].as_array().unwrap() {
    let items: Vec<StoreItem> = session["turns"].as_array().unwrap()
        .iter()
        .map(|turn| StoreItem {
            text: turn["text"].as_str().unwrap().to_string(),
            topic_label: Some(session["id"].as_str().unwrap().to_string()),
            // ... other fields
        })
        .collect();
    
    // Call batch_store
    let cmd = json!({
        "command": "batch_store",
        "items": items,
        "session_id": session["id"]
    });
    // memhop_execute(db, &cmd);
}

// Evaluate search
for question in dataset["questions"].as_array().unwrap() {
    let search_cmd = json!({
        "command": "search",
        "dialogue": question["question"],
        "context_limit": 5
    });
    // Compare results with question["answer"]
}
```
