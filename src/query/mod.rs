// Query module
//
// Module organization (after P0 cleanup):
// - NEW API (recommended): search, update, import, list, merge, update_title, types
// - INTERNAL USE: batch, l0_crud, slot_io

pub mod batch;           // Internal: Batch storage operations
pub mod import;          // NEW API: import_memory implementation
pub mod l0_crud;         // Internal: L0 Profile CRUD operations (unified implementation)
pub mod list;            // NEW API: L0-L5 list queries
pub mod merge;           // NEW API: merge_l2_topics implementation
pub mod search;          // NEW API: search_memory implementation (L2-centric retrieval)
pub mod slot_io;         // Internal: Unified Slot I/O utilities (eliminates duplication)
pub mod types;           // NEW API: Public type definitions
pub mod update;          // NEW API: update_memory implementation
pub mod update_title;    // NEW API: Title/profile update interfaces
