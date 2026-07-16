<p align="center">
  <h1 align="center">MemHop</h1>
  <p align="center">
    <strong>Your agent remembers like a human.</strong>
  </p>
  <p align="center">
    A memory database purpose-built for AI agents, implementing a seven-layer cognitive architecture (L0–L6) in a single embedded file.
  </p>
  <p align="center">
    <a href="https://qyiun666.github.io/meowagent.github.io/">Website</a>
    &middot;
    <a href="https://github.com/meowagent/meowagent">MeowAgent</a>
  </p>
</p>

<p align="center">
  <img src="https://img.shields.io/github/go-mod/go-version/qyiun666/memhop" alt="go">
  <img src="https://img.shields.io/github/license/qyiun666/memhop" alt="license">
</p>

---

MemHop is not a vector database. It is a memory system modeled after how the human brain organizes knowledge — with identity, episodic recall, semantic compression, skill acquisition, archival storage, and crystallized expertise. One agent, one `.meh` file, zero infrastructure.

Built as the brain memory of [MeowAgent](https://github.com/meowagent/meowagent), MemHop works as an embedded organ rather than a standalone service. No server to run, no configuration to manage — just open a file and your agent has memory.

## Quick Start

### Go (current)

```go
import "github.com/qyiun666/memhop/memhop"

db, err := memhop.Open(&memhop.Config{
    DBPath:      "agent.meh",
    VectorDim:   768,
    EncoderAddr: "http://127.0.0.1:11434",
    EmbedModel:  "nomic-embed-text",
    LLM: core.LlmConfig{
        APIURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        Model:  "gpt-4o-mini",
    },
})
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// 1. Retrieve relevant contexts
results, err := db.Search(memhop.SearchQuery{
    Text:         "What did we discuss yesterday?",
    ContextLimit: 10,
})
if err != nil {
    log.Fatal(err)
}

for _, ctx := range results.Contexts {
    fmt.Printf("[%.2f] %s\n", ctx.RetrievalScore, ctx.FusedSummary)
}

// 2. Run Dream consolidation
report, err := db.Dream(nil)
fmt.Printf("dream stages: %d\n", report.ConsolidatedCount)
```

<details>
<summary>Rust version (v0.18 – v0.61)</summary>

```rust
use memhop::{MemHop, MemHopConfig, SearchQuery, UpdateRequest};

let mut db = MemHop::open(MemHopConfig::new("agent.meh".into(), 768))?;

// 1. Retrieve relevant contexts
let results = db.search_context(SearchQuery {
    dialogue: "What did we discuss yesterday?".into(),
    l2_id: None,
    context_id: None,
    l3_id: None,
    context_limit: 10,
    auto_create: 0,
    min_score: 0.0,
    source: Default::default(),
})?;

for ctx in &results.contexts {
    println!("[{:.2}] {}", ctx.retrieval_score,
        ctx.fused_summary.as_deref().unwrap_or(""));
}

// 2. Append a new turn
if let Some(ctx) = results.contexts.first() {
    db.update_memory(UpdateRequest {
        topic_id: ctx.id.clone(),
        dialogue_text: "We discussed Rust lifetimes.".into(),
        summary: None,
        action_chain: None,
        instant_distill: false,
        source: Default::default(),
    })?;
}

// 3. Run Dream consolidation
let report = db.dream(None)?;
println!("dream stages: {:?}", report.stages);

// 4. Graceful shutdown
db.close()?;
```

</details>

<details>
<summary>Python version (v0.1 – v0.5)</summary>

```python
import memhop

with memhop.open("brain.db") as db:
    # Write memory
    id = db.remember("今天吃了豆浆油条", meta={"tags": ["早餐"], "session_id": "s01"})

    # O(1) associative recall
    m = db.recall("早餐吃了什么")
    print(m.text)        # "今天吃了豆浆油条"
    print(m.confidence)  # 0.94

    # Spread activation from seed
    results = db.spread_activation("God Object", max_hops=2)
    # → [{id: "...", activation: 0.85}, {id: "...", activation: 0.42}]
```

</details>

## Architecture

MemHop models memory as seven cognitive layers, each corresponding to a distinct brain function. Memories flow between layers during the Dream consolidation cycle, just as the human brain consolidates experiences during sleep.

```
Layer   Name             Human Parallel          Mechanism
─────   ──────────────   ───────────────────     ─────────────────────────────────────────────
 L6     PathwayWeight    Procedural memory       Weighted action pathways & habit reinforcement
 L5     Crystal          Muscle memory           Crystallized procedures & reusable skills
 L4     Archive          Long-term memory        Raw dialogue logs & historical records
 L3     Knowledge        Semantic memory         Multi-source hypergraph knowledge base
 L2     Context          Working memory          Compressed topic structures (4 depth levels)
 L1     Engram           Associative hypergraph  Hypergraph skeleton linking L2 contexts
 L0     Profile          Identity                Agent personality, preferences & language habits
```

### Knowledge Graph (L3)

L3 stores structured knowledge as **multiple independent hypergraphs** — not flat embeddings, but typed nodes connected by labeled, weighted edges. Knowledge can be distilled from conversations (Dream pipeline), imported from documents, or created programmatically.

### Dream Pipeline

The Dream cycle is an automatic memory consolidation process inspired by how the human brain processes experiences during sleep:

1. **L3 Distillation** — Extract structured knowledge via LLM
2. **L2 Compression** — Demote old contexts, merge topics
3. **L1 Rebuild & Decay** — Rebuild hypergraph, decay episodic importance
4. **L0 Profile Rebuild** — Regenerate agent profile
5. **Language Habit Learning** — Discover vocabulary and style patterns
6. **L5 Crystallization** — Extract reusable procedures
7. **L6 Pathway Decay** — Apply time-decay to pathway weights

### Search

MemHop uses **two-channel retrieval fusion** (BM25 + vector) with RRF (Reciprocal Rank Fusion):

| Channel | Weight | Method |
|---------|--------|--------|
| BM25 | 0.45 | Keyword matching via inverted index (gojieba/gse CJK tokenization) |
| Vector | 0.55 | Semantic similarity with f16 half-precision via Ollama HTTP API |

## Development

```bash
go build ./memhop/...          # Build
go test ./memhop/...           # Unit tests
go test ./test/...             # Integration tests (requires Ollama)
go vet ./...                   # Static analysis

# Or use Makefile
make build
make test
make test-unit
make test-integration
make bench
```

### Prerequisites

- Go 1.25+
- Ollama running locally (`ollama serve`) with embedding model (`ollama pull nomic-embed-text`)
- CGO_ENABLED=1 for gojieba tokenizer (auto-fallback to gse without CGO)

## Changelog

| Version | Language | Highlights |
|---|---|---|
| **v0.57.0+** | Go | Go rewrite: HTTP Ollama encoder, log/slog, RRF fusion, 3 deps only |
| **v0.18–v0.61** | Rust | V2 append-only .meh, BM25+IVF, L3 hypergraph DSL, Dream pipeline |
| **v0.6–v0.17** | Rust | Pure Rust crate, LMDB → .meh migration, MCP server |
| **v0.1–v0.5** | Python | Hopfield network, LMDB, pip install, associative recall |

## License

Licensed under either of [Apache License, Version 2.0](LICENSE-APACHE) or [MIT License](LICENSE-MIT) at your option.
