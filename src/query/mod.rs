// Query module
//
// Module organization (after P0 cleanup):
// - NEW API (recommended): search, update, import, list, merge, update_title, types
// - INTERNAL USE: batch, l0_crud, slot_io, common

pub mod batch; // Internal: Batch storage operations
pub mod common; // Internal: Common utility functions (eliminates duplication)
pub mod import; // NEW API: import_memory implementation
pub mod l0_crud; // Internal: L0 Profile CRUD operations (unified implementation)
pub mod list; // NEW API: list queries
pub mod merge; // NEW API: merge_topics implementation
pub mod search; // NEW API: search_memory implementation (topic-centric retrieval)
pub mod slot_io; // Internal: Unified Slot I/O utilities (eliminates duplication)
pub mod types; // NEW API: Public type definitions
pub mod update; // NEW API: update_memory implementation
pub mod update_title; // NEW API: Title/profile update interfaces
