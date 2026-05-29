//! LMDB storage layer for the Brain — 9 sub-databases.
//!
//! Sub-databases:
//!   engrams         — id → bincode(Engram)
//!   hippocampus     — id → bincode(Engram)
//!   graph_edges     — source_id → bincode(Vec<Association>)
//!   schemas         — id → bincode(SchemaExtra)
//!   anchor_index    — anchor_name → bincode(Vec<engram_id>)
//!   config          — key → bincode(value)
//!   dialogue_turns  — turn_id → bincode(DialogueTurn)     (v0.8.0)
//!   plan_tree       — plan_id → bincode(PlanNode)         (v0.8.0)
//!   hnsw_index      — "hnsw" → bincode(HnswIndex bytes)   (v0.9.0)

#![allow(dead_code)]

use heed::types::{Bytes, Str};
use heed::{Database, Env, EnvOpenOptions, RoTxn, RwTxn};
use std::path::Path;

use crate::engram::{Association, DialogueTurn, Engram, PlanNode, SchemaExtra};
use crate::plan_gate::PlanIndex;

// ── Schema version ──────────────────────────────────────────

/// Current LMDB schema version for v0.11.0.
pub const CURRENT_SCHEMA: &str = "0.11.0";

// ── StorageError ──────────────────────────────────────────────

#[derive(Debug)]
pub enum StorageError {
    Open(String),
    Read(String),
    Write(String),
    Close(String),
}

impl std::fmt::Display for StorageError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            StorageError::Open(msg) => write!(f, "open: {}", msg),
            StorageError::Read(msg) => write!(f, "read: {}", msg),
            StorageError::Write(msg) => write!(f, "write: {}", msg),
            StorageError::Close(msg) => write!(f, "close: {}", msg),
        }
    }
}

impl std::error::Error for StorageError {}

impl From<heed::Error> for StorageError {
    fn from(err: heed::Error) -> Self {
        StorageError::Open(err.to_string())
    }
}

// ── Sub-database handles ─────────────────────────────────────

pub(crate) struct BrainDb {
    pub engrams: Database<Str, Bytes>,
    pub hippocampus: Database<Str, Bytes>,
    pub graph_edges: Database<Str, Bytes>,
    pub schemas: Database<Str, Bytes>,
    pub anchor_index: Database<Str, Bytes>,
    pub config: Database<Str, Bytes>,
    /// v0.8.0: turn_id → bincode(DialogueTurn)
    pub dialogue_turns: Database<Str, Bytes>,
    /// v0.8.0: plan_id → bincode(PlanNode)
    pub plan_tree: Database<Str, Bytes>,
    /// v0.9.0: "hnsw" → bincode(HnswIndex serialized bytes)
    pub hnsw_index: Database<Str, Bytes>,
}

// ── LmdbStorage ──────────────────────────────────────────────

/// LMDB storage for the Brain — manages open/close/read/write for 8 sub-dbs.
pub struct LmdbStorage {
    env: Env,
    pub(crate) db: BrainDb,
    /// v0.8.0: In-memory auxiliary index for fast plan lookups.
    pub(crate) plan_index: PlanIndex,
}

unsafe impl Send for LmdbStorage {}
unsafe impl Sync for LmdbStorage {}

impl LmdbStorage {
    /// Open (or create) an LMDB environment at `path` and create/open the 8 sub-databases.
    pub fn open(path: &str) -> Result<Self, StorageError> {
        let db_path = Path::new(path);
        std::fs::create_dir_all(db_path)
            .map_err(|e| StorageError::Open(format!("create dir {}: {}", path, e)))?;
        let env = unsafe {
            EnvOpenOptions::new()
                .map_size(2 * 1024 * 1024 * 1024)
                .max_readers(128)
                .max_dbs(10)
                .open(db_path)
                .map_err(|e| StorageError::Open(format!("env open: {}", e)))?
        };

        let mut wtxn = env
            .write_txn()
            .map_err(|e| StorageError::Open(format!("write txn: {}", e)))?;

        let engrams = env
            .create_database(&mut wtxn, Some("engrams"))
            .map_err(|e| StorageError::Open(format!("engrams db: {}", e)))?;
        let hippocampus = env
            .create_database(&mut wtxn, Some("hippocampus"))
            .map_err(|e| StorageError::Open(format!("hippocampus db: {}", e)))?;
        let graph_edges = env
            .create_database(&mut wtxn, Some("graph_edges"))
            .map_err(|e| StorageError::Open(format!("graph_edges db: {}", e)))?;
        let schemas = env
            .create_database(&mut wtxn, Some("schemas"))
            .map_err(|e| StorageError::Open(format!("schemas db: {}", e)))?;
        let anchor_index = env
            .create_database(&mut wtxn, Some("anchor_index"))
            .map_err(|e| StorageError::Open(format!("anchor_index db: {}", e)))?;
        let config = env
            .create_database(&mut wtxn, Some("config"))
            .map_err(|e| StorageError::Open(format!("config db: {}", e)))?;
        let dialogue_turns = env
            .create_database(&mut wtxn, Some("dialogue_turns"))
            .map_err(|e| StorageError::Open(format!("dialogue_turns db: {}", e)))?;
        let plan_tree = env
            .create_database(&mut wtxn, Some("plan_tree"))
            .map_err(|e| StorageError::Open(format!("plan_tree db: {}", e)))?;
        let hnsw_index: Database<Str, Bytes> = env
            .create_database(&mut wtxn, Some("hnsw_index"))
            .map_err(|e| StorageError::Open(format!("hnsw_index db: {}", e)))?;

        wtxn
            .commit()
            .map_err(|e| StorageError::Open(format!("commit: {}", e)))?;

        Ok(LmdbStorage {
            env,
            db: BrainDb {
                engrams,
                hippocampus,
                graph_edges,
                schemas,
                anchor_index,
                config,
                dialogue_turns,
                plan_tree,
                hnsw_index,
            },
            plan_index: PlanIndex::new(),
        })
    }

    pub fn begin_read(&self) -> Result<RoTxn<'_>, StorageError> {
        self.env
            .read_txn()
            .map_err(|e| StorageError::Read(format!("begin read: {}", e)))
    }

    pub fn begin_write(&self) -> Result<RwTxn<'_>, StorageError> {
        self.env
            .write_txn()
            .map_err(|e| StorageError::Write(format!("begin write: {}", e)))
    }

    pub fn close(&self) -> Result<(), StorageError> {
        Ok(())
    }

    // ── Engram read/write ──────────────────────────────────────

    pub fn put_engram(
        &self,
        txn: &mut RwTxn<'_>,
        id: &str,
        engram: &Engram,
    ) -> Result<(), StorageError> {
        let bytes = bincode::serialize(engram)
            .map_err(|e| StorageError::Write(format!("serialize engram: {}", e)))?;
        self.db
            .engrams
            .put(txn, id, &bytes)
            .map_err(|e| StorageError::Write(format!("put engram: {}", e)))
    }

    pub fn get_engram(
        &self,
        txn: &RoTxn<'_>,
        id: &str,
    ) -> Result<Option<Engram>, StorageError> {
        match self.db.engrams.get(txn, id) {
            Ok(Some(bytes)) => {
                let engram: Engram = bincode::deserialize(bytes)
                    .map_err(|e| StorageError::Read(format!("deserialize engram: {}", e)))?;
                Ok(Some(engram))
            }
            Ok(None) => Ok(None),
            Err(e) => Err(StorageError::Read(format!("get engram: {}", e))),
        }
    }

    pub fn delete_engram(
        &self,
        txn: &mut RwTxn<'_>,
        id: &str,
    ) -> Result<bool, StorageError> {
        self.db
            .engrams
            .delete(txn, id)
            .map_err(|e| StorageError::Write(format!("delete engram: {}", e)))
    }

    pub fn engram_exists(
        &self,
        txn: &RoTxn<'_>,
        id: &str,
    ) -> Result<bool, StorageError> {
        self.db.engrams.get(txn, id).map(|v| v.is_some()).map_err(|e| {
            StorageError::Read(format!("engram exists: {}", e))
        })
    }

    /// Iterate all engram IDs in the engrams database.
    pub fn all_engram_ids(&self, txn: &RoTxn<'_>) -> Result<Vec<String>, StorageError> {
        let mut ids = Vec::new();
        let iter = self
            .db
            .engrams
            .iter(txn)
            .map_err(|e| StorageError::Read(format!("iter engrams: {}", e)))?;
        for result in iter {
            let (key, _) = result.map_err(|e| StorageError::Read(format!("iter: {}", e)))?;
            ids.push(key.to_string());
        }
        Ok(ids)
    }

    /// Iterate all engrams (id + value).
    pub fn all_engrams(&self, txn: &RoTxn<'_>) -> Result<Vec<(String, Engram)>, StorageError> {
        let mut out = Vec::new();
        let iter = self
            .db
            .engrams
            .iter(txn)
            .map_err(|e| StorageError::Read(format!("iter engrams: {}", e)))?;
        for result in iter {
            let (key, val) = result.map_err(|e| StorageError::Read(format!("iter: {}", e)))?;
            let engram: Engram = bincode::deserialize(val)
                .map_err(|e| StorageError::Read(format!("deserialize: {}", e)))?;
            out.push((key.to_string(), engram));
        }
        Ok(out)
    }

    // ── Hippocampus read/write ────────────────────────────────

    pub fn put_hippocampus(
        &self,
        txn: &mut RwTxn<'_>,
        id: &str,
        engram: &Engram,
    ) -> Result<(), StorageError> {
        let bytes = bincode::serialize(engram)
            .map_err(|e| StorageError::Write(format!("serialize hippocampus: {}", e)))?;
        self.db
            .hippocampus
            .put(txn, id, &bytes)
            .map_err(|e| StorageError::Write(format!("put hippocampus: {}", e)))
    }

    pub fn get_hippocampus(
        &self,
        txn: &RoTxn<'_>,
        id: &str,
    ) -> Result<Option<Engram>, StorageError> {
        match self.db.hippocampus.get(txn, id) {
            Ok(Some(bytes)) => {
                let engram: Engram = bincode::deserialize(bytes)
                    .map_err(|e| StorageError::Read(format!("deserialize hippocampus: {}", e)))?;
                Ok(Some(engram))
            }
            Ok(None) => Ok(None),
            Err(e) => Err(StorageError::Read(format!("get hippocampus: {}", e))),
        }
    }

    pub fn delete_hippocampus(
        &self,
        txn: &mut RwTxn<'_>,
        id: &str,
    ) -> Result<bool, StorageError> {
        self.db
            .hippocampus
            .delete(txn, id)
            .map_err(|e| StorageError::Write(format!("delete hippocampus: {}", e)))
    }

    pub fn hippocampus_len(&self, txn: &RoTxn<'_>) -> Result<u64, StorageError> {
        self.db
            .hippocampus
            .len(txn)
            .map_err(|e| StorageError::Read(format!("hippocampus len: {}", e)))
    }

    /// Iterate all hippocampus entries.
    pub fn all_hippocampus_entries(
        &self,
        txn: &RoTxn<'_>,
    ) -> Result<Vec<(String, Engram)>, StorageError> {
        let mut entries = Vec::new();
        let iter = self
            .db
            .hippocampus
            .iter(txn)
            .map_err(|e| StorageError::Read(format!("iter hippocampus: {}", e)))?;
        for result in iter {
            let (key, val) = result.map_err(|e| StorageError::Read(format!("iter: {}", e)))?;
            let engram: Engram = bincode::deserialize(val)
                .map_err(|e| StorageError::Read(format!("deserialize: {}", e)))?;
            entries.push((key.to_string(), engram));
        }
        Ok(entries)
    }

    // ── Graph edges read/write ─────────────────────────────────

    pub fn put_edges(
        &self,
        txn: &mut RwTxn<'_>,
        source_id: &str,
        edges: &[Association],
    ) -> Result<(), StorageError> {
        let bytes = bincode::serialize(edges)
            .map_err(|e| StorageError::Write(format!("serialize edges: {}", e)))?;
        self.db
            .graph_edges
            .put(txn, source_id, &bytes)
            .map_err(|e| StorageError::Write(format!("put edges: {}", e)))
    }

    pub fn get_edges(
        &self,
        txn: &RoTxn<'_>,
        source_id: &str,
    ) -> Result<Option<Vec<Association>>, StorageError> {
        match self.db.graph_edges.get(txn, source_id) {
            Ok(Some(bytes)) => {
                let edges: Vec<Association> = bincode::deserialize(bytes)
                    .map_err(|e| StorageError::Read(format!("deserialize edges: {}", e)))?;
                Ok(Some(edges))
            }
            Ok(None) => Ok(None),
            Err(e) => Err(StorageError::Read(format!("get edges: {}", e))),
        }
    }

    pub fn delete_edges(
        &self,
        txn: &mut RwTxn<'_>,
        source_id: &str,
    ) -> Result<bool, StorageError> {
        self.db
            .graph_edges
            .delete(txn, source_id)
            .map_err(|e| StorageError::Write(format!("delete edges: {}", e)))
    }

    // ── Schema read/write ─────────────────────────────────────

    pub fn put_schema(
        &self,
        txn: &mut RwTxn<'_>,
        id: &str,
        schema: &SchemaExtra,
    ) -> Result<(), StorageError> {
        let bytes = bincode::serialize(schema)
            .map_err(|e| StorageError::Write(format!("serialize schema: {}", e)))?;
        self.db
            .schemas
            .put(txn, id, &bytes)
            .map_err(|e| StorageError::Write(format!("put schema: {}", e)))
    }

    pub fn get_schema(
        &self,
        txn: &RoTxn<'_>,
        id: &str,
    ) -> Result<Option<SchemaExtra>, StorageError> {
        match self.db.schemas.get(txn, id) {
            Ok(Some(bytes)) => {
                let schema: SchemaExtra = bincode::deserialize(bytes)
                    .map_err(|e| StorageError::Read(format!("deserialize schema: {}", e)))?;
                Ok(Some(schema))
            }
            Ok(None) => Ok(None),
            Err(e) => Err(StorageError::Read(format!("get schema: {}", e))),
        }
    }

    pub fn delete_schema(
        &self,
        txn: &mut RwTxn<'_>,
        id: &str,
    ) -> Result<bool, StorageError> {
        self.db
            .schemas
            .delete(txn, id)
            .map_err(|e| StorageError::Write(format!("delete schema: {}", e)))
    }

    pub fn all_schema_ids(&self, txn: &RoTxn<'_>) -> Result<Vec<String>, StorageError> {
        let mut ids = Vec::new();
        let iter = self
            .db
            .schemas
            .iter(txn)
            .map_err(|e| StorageError::Read(format!("iter schemas: {}", e)))?;
        for result in iter {
            let (key, _) = result.map_err(|e| StorageError::Read(format!("iter: {}", e)))?;
            ids.push(key.to_string());
        }
        Ok(ids)
    }

    // ── Anchor index read/write ───────────────────────────────

    pub fn put_anchor_engrams(
        &self,
        txn: &mut RwTxn<'_>,
        anchor_name: &str,
        ids: &[String],
    ) -> Result<(), StorageError> {
        let bytes = bincode::serialize(ids)
            .map_err(|e| StorageError::Write(format!("serialize anchor: {}", e)))?;
        self.db
            .anchor_index
            .put(txn, anchor_name, &bytes)
            .map_err(|e| StorageError::Write(format!("put anchor: {}", e)))
    }

    pub fn get_anchor_engrams(
        &self,
        txn: &RoTxn<'_>,
        anchor_name: &str,
    ) -> Result<Option<Vec<String>>, StorageError> {
        match self.db.anchor_index.get(txn, anchor_name) {
            Ok(Some(bytes)) => {
                let ids: Vec<String> = bincode::deserialize(bytes)
                    .map_err(|e| StorageError::Read(format!("deserialize anchor: {}", e)))?;
                Ok(Some(ids))
            }
            Ok(None) => Ok(None),
            Err(e) => Err(StorageError::Read(format!("get anchor: {}", e))),
        }
    }

    pub fn delete_anchor(
        &self,
        txn: &mut RwTxn<'_>,
        anchor_name: &str,
    ) -> Result<bool, StorageError> {
        self.db
            .anchor_index
            .delete(txn, anchor_name)
            .map_err(|e| StorageError::Write(format!("delete anchor: {}", e)))
    }

    pub fn all_anchor_names(&self, txn: &RoTxn<'_>) -> Result<Vec<String>, StorageError> {
        let mut names = Vec::new();
        let iter = self
            .db
            .anchor_index
            .iter(txn)
            .map_err(|e| StorageError::Read(format!("iter anchors: {}", e)))?;
        for result in iter {
            let (key, _) = result.map_err(|e| StorageError::Read(format!("iter: {}", e)))?;
            names.push(key.to_string());
        }
        Ok(names)
    }

    // ── Config read/write ─────────────────────────────────────

    pub fn put_config<T: serde::Serialize>(
        &self,
        txn: &mut RwTxn<'_>,
        key: &str,
        value: &T,
    ) -> Result<(), StorageError> {
        let bytes = bincode::serialize(value)
            .map_err(|e| StorageError::Write(format!("serialize: {}", e)))?;
        self.db
            .config
            .put(txn, key, &bytes)
            .map_err(|e| StorageError::Write(format!("put config: {}", e)))
    }

    pub fn get_config<T: serde::de::DeserializeOwned>(
        &self,
        txn: &RoTxn<'_>,
        key: &str,
    ) -> Result<Option<T>, StorageError> {
        match self.db.config.get(txn, key) {
            Ok(Some(bytes)) => {
                let val: T = bincode::deserialize(bytes)
                    .map_err(|e| StorageError::Read(format!("deserialize config: {}", e)))?;
                Ok(Some(val))
            }
            Ok(None) => Ok(None),
            Err(e) => Err(StorageError::Read(format!("get config: {}", e))),
        }
    }

    // ── DialogueTurn read/write (v0.8.0) ─────────────────────

    pub fn put_dialogue(
        &self,
        txn: &mut RwTxn<'_>,
        turn: &DialogueTurn,
    ) -> Result<(), StorageError> {
        let bytes = bincode::serialize(turn)
            .map_err(|e| StorageError::Write(format!("serialize dialogue: {}", e)))?;
        self.db
            .dialogue_turns
            .put(txn, &turn.id as &str, &bytes)
            .map_err(|e| StorageError::Write(format!("put dialogue: {}", e)))
    }

    pub fn get_dialogue(
        &self,
        txn: &RoTxn<'_>,
        id: &str,
    ) -> Result<Option<DialogueTurn>, StorageError> {
        match self.db.dialogue_turns.get(txn, id) {
            Ok(Some(bytes)) => {
                let turn: DialogueTurn = bincode::deserialize(bytes)
                    .map_err(|e| StorageError::Read(format!("deserialize dialogue: {}", e)))?;
                Ok(Some(turn))
            }
            Ok(None) => Ok(None),
            Err(e) => Err(StorageError::Read(format!("get dialogue: {}", e))),
        }
    }

    /// Get all DialogueTurns belonging to a plan, sorted by timestamp.
    pub fn get_dialogues_by_plan(
        &self,
        txn: &RoTxn<'_>,
        plan_id: &str,
    ) -> Result<Vec<DialogueTurn>, StorageError> {
        let mut turns: Vec<DialogueTurn> = Vec::new();
        let iter = self
            .db
            .dialogue_turns
            .iter(txn)
            .map_err(|e| StorageError::Read(format!("iter dialogues: {}", e)))?;
        for result in iter {
            let (_, val) = result.map_err(|e| StorageError::Read(format!("iter: {}", e)))?;
            let turn: DialogueTurn = bincode::deserialize(val)
                .map_err(|e| StorageError::Read(format!("deserialize: {}", e)))?;
            if turn.plan_id == plan_id {
                turns.push(turn);
            }
        }
        turns.sort_by_key(|t| t.timestamp);
        Ok(turns)
    }

    /// Get all DialogueTurns (full scan).
    pub fn all_dialogues(&self, txn: &RoTxn<'_>) -> Result<Vec<DialogueTurn>, StorageError> {
        let mut turns: Vec<DialogueTurn> = Vec::new();
        let iter = self
            .db
            .dialogue_turns
            .iter(txn)
            .map_err(|e| StorageError::Read(format!("iter dialogues: {}", e)))?;
        for result in iter {
            let (_, val) = result.map_err(|e| StorageError::Read(format!("iter: {}", e)))?;
            let turn: DialogueTurn = bincode::deserialize(val)
                .map_err(|e| StorageError::Read(format!("deserialize: {}", e)))?;
            turns.push(turn);
        }
        Ok(turns)
    }

    /// v0.9.1: Delete a DialogueTurn by ID.
    pub fn delete_dialogue(
        &self,
        txn: &mut RwTxn<'_>,
        turn_id: &str,
    ) -> Result<bool, StorageError> {
        self.db.dialogue_turns.delete(txn, turn_id)
            .map_err(|e| StorageError::Write(format!("delete dialogue: {}", e)))
    }

    // ── PlanNode read/write (v0.8.0) ──────────────────────────

    pub fn put_plan(
        &self,
        txn: &mut RwTxn<'_>,
        plan: &PlanNode,
    ) -> Result<(), StorageError> {
        let bytes = bincode::serialize(plan)
            .map_err(|e| StorageError::Write(format!("serialize plan: {}", e)))?;
        self.db
            .plan_tree
            .put(txn, &plan.id as &str, &bytes)
            .map_err(|e| StorageError::Write(format!("put plan: {}", e)))
    }

    pub fn get_plan(
        &self,
        txn: &RoTxn<'_>,
        id: &str,
    ) -> Result<Option<PlanNode>, StorageError> {
        match self.db.plan_tree.get(txn, id) {
            Ok(Some(bytes)) => {
                let plan: PlanNode = bincode::deserialize(bytes)
                    .map_err(|e| StorageError::Read(format!("deserialize plan: {}", e)))?;
                Ok(Some(plan))
            }
            Ok(None) => Ok(None),
            Err(e) => Err(StorageError::Read(format!("get plan: {}", e))),
        }
    }

    /// Iterate all PlanNode entries.
    pub fn get_all_plans(&self, txn: &RoTxn<'_>) -> Result<Vec<PlanNode>, StorageError> {
        let mut plans: Vec<PlanNode> = Vec::new();
        let iter = self
            .db
            .plan_tree
            .iter(txn)
            .map_err(|e| StorageError::Read(format!("iter plans: {}", e)))?;
        for result in iter {
            let (_, val) = result.map_err(|e| StorageError::Read(format!("iter: {}", e)))?;
            let plan: PlanNode = bincode::deserialize(val)
                .map_err(|e| StorageError::Read(format!("deserialize: {}", e)))?;
            plans.push(plan);
        }
        Ok(plans)
    }

    pub fn delete_plan(
        &self,
        txn: &mut RwTxn<'_>,
        id: &str,
    ) -> Result<bool, StorageError> {
        self.db
            .plan_tree
            .delete(txn, id)
            .map_err(|e| StorageError::Write(format!("delete plan: {}", e)))
    }

    // ── HNSW index read/write (v0.9.0) ─────────────────────────

    /// Store the serialized HNSW index blob under the key "hnsw".
    pub fn put_hnsw_index(
        &self,
        txn: &mut RwTxn<'_>,
        data: &[u8],
    ) -> Result<(), StorageError> {
        self.db
            .hnsw_index
            .put(txn, "hnsw", data)
            .map_err(|e| StorageError::Write(format!("put hnsw: {}", e)))
    }

    /// Retrieve the serialized HNSW index blob.
    pub fn get_hnsw_index(
        &self,
        txn: &RoTxn<'_>,
    ) -> Result<Option<Vec<u8>>, StorageError> {
        self.db
            .hnsw_index
            .get(txn, "hnsw")
            .map(|opt| opt.map(|bytes| bytes.to_vec()))
            .map_err(|e| StorageError::Read(format!("get hnsw: {}", e)))
    }

    /// Delete the HNSW index from storage.
    pub fn delete_hnsw_index(
        &self,
        txn: &mut RwTxn<'_>,
    ) -> Result<bool, StorageError> {
        self.db
            .hnsw_index
            .delete(txn, "hnsw")
            .map_err(|e| StorageError::Write(format!("delete hnsw: {}", e)))
    }
}

impl LmdbStorage {
    /// Get all engram IDs for an anchor (empty vec if anchor doesn't exist).
    pub fn anchor_get_ids(
        &self,
        txn: &RoTxn<'_>,
        name: &str,
    ) -> Result<Vec<String>, StorageError> {
        Ok(self.get_anchor_engrams(txn, name)?.unwrap_or_default())
    }

    /// Compute the candidate set for a set of anchors (union).
    /// Returns Ok(None) when anchors is empty.
    pub fn anchor_candidates(
        &self,
        txn: &RoTxn<'_>,
        names: &[String],
    ) -> Result<Option<std::collections::HashSet<String>>, StorageError> {
        if names.is_empty() {
            return Ok(None);
        }
        let mut result = std::collections::HashSet::new();
        for name in names {
            for id in self.anchor_get_ids(txn, name)? {
                result.insert(id);
            }
        }
        Ok(Some(result))
    }

    /// Add an engram ID to an anchor (creates the anchor if it doesn't exist).
    pub fn anchor_add(
        &self,
        txn: &mut RwTxn<'_>,
        anchor: &str,
        engram_id: &str,
    ) -> Result<(), StorageError> {
        let mut ids = self.get_anchor_engrams(txn, anchor)?.unwrap_or_default();
        if !ids.contains(&engram_id.to_string()) {
            ids.push(engram_id.to_string());
            self.put_anchor_engrams(txn, anchor, &ids)?;
        }
        Ok(())
    }

    /// Remove an engram ID from an anchor.
    pub fn anchor_remove(
        &self,
        txn: &mut RwTxn<'_>,
        anchor: &str,
        engram_id: &str,
    ) -> Result<(), StorageError> {
        let mut ids = self.get_anchor_engrams(txn, anchor)?.unwrap_or_default();
        let before = ids.len();
        ids.retain(|id| id != engram_id);
        if ids.is_empty() {
            self.delete_anchor(txn, anchor)?;
        } else if ids.len() < before {
            self.put_anchor_engrams(txn, anchor, &ids)?;
        }
        Ok(())
    }
}
