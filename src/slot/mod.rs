// Slot serialization module
//
// L0: Profile (JSON format - agent identity)
// L1: ContextNode + Hyperedge (hypergraph skeleton, nodes = L2 contexts)
// L2: ContextSlot (scene-based conversation context, 3-level nesting)
// L3: HypergraphSlot + HypergraphNode + HypergraphEdge (generic hypergraph engine)
// L4: ArchiveSlot (raw text + file paths, minimalist)
// L5: ActionChainSlot + ActionStep (ordered action sequences)

pub mod profile;        // L0 agent profile (JSON)
pub mod context_node;   // L1 graph node (points to L2)
pub mod hyperedge;      // L1 hyperedge (connects L1 nodes)
pub mod context;        // L2 scene context slot
pub mod hypergraph;     // L3 generic hypergraph container + node + edge
pub mod archive;        // L4 raw archive storage
pub mod action_chain;   // L5 action chain + steps
