// Slot serialization module
//
// L0: Profile (JSON format - agent identity)
// L1: Engram + Hyperedge (episodic memories)
// L2: Topic + TopicEdge (semantic compression)
// L3: Knowledge + KnowledgeEdge (domain knowledge graphs)
// L4: Archive (raw text + file paths)
// L5: Crystal (programmatic knowledge + operation flows)

pub mod archive;      // L4 raw archive storage
pub mod crystal;      // L5 crystallized knowledge + operation flows
pub mod engram;       // L1 episodic memory slot
pub mod hyperedge;    // L1 hyperedge (connects engrams)
pub mod knowledge;    // L3 knowledge node + edge
pub mod profile;      // L0 agent profile (JSON)
pub mod topic;        // L2 topic slot
