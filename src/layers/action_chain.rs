// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L5 ActionChain — procedural knowledge as ordered action sequences.
//! Replaces the old CrystalSlot which crammed everything into a `raw_steps` blob.

use crate::util::io_helpers::*;
use std::io::{self, Cursor, Read, Write};

// ============================================================================
// ChainStatus
// ============================================================================

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum ChainStatus {
    Draft = 0,      // Not yet validated
    Active = 1,     // Verified and available
    Deprecated = 2, // No longer recommended
}

impl ChainStatus {
    pub fn from_u8(v: u8) -> Self {
        match v {
            0 => Self::Draft,
            1 => Self::Active,
            2 => Self::Deprecated,
            _ => Self::Draft,
        }
    }
}

// ============================================================================
// ActionChainSlot — chain metadata
// ============================================================================

#[derive(Debug, Clone, PartialEq)]
pub struct ActionChainSlot {
    pub id_hash: u64,
    pub title: String,
    pub trigger: String, // Trigger condition (DSL or natural language)
    pub status: ChainStatus,
    pub confidence: f32,
    pub success_rate: f32,
    pub trigger_count: u32,
    pub last_triggered: i64,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}

impl ActionChainSlot {
    /// Fixed 53 bytes + variable `title.len() + trigger.len()`.
    pub fn slot_size(&self) -> usize {
        53 + self.title.len() + self.trigger.len()
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());
        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&(self.title.len() as u16).to_le_bytes())?;
        buf.write_all(&(self.trigger.len() as u16).to_le_bytes())?;
        buf.write_all(&[self.status as u8])?;
        buf.write_all(&self.confidence.to_le_bytes())?;
        buf.write_all(&self.success_rate.to_le_bytes())?;
        buf.write_all(&self.trigger_count.to_le_bytes())?;
        buf.write_all(&self.last_triggered.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        buf.write_all(&self.updated_at.to_le_bytes())?;
        buf.write_all(&self.version.to_le_bytes())?;
        buf.write_all(self.title.as_bytes())?;
        buf.write_all(self.trigger.as_bytes())?;
        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);
        let id_hash = read_u64(&mut c)?;
        let title_len = read_u16(&mut c)? as usize;
        let trigger_len = read_u16(&mut c)? as usize;
        let status = ChainStatus::from_u8(read_u8(&mut c)?);
        let confidence = read_f32(&mut c)?;
        let success_rate = read_f32(&mut c)?;
        let trigger_count = read_u32(&mut c)?;
        let last_triggered = read_i64(&mut c)?;
        let created_at = read_i64(&mut c)?;
        let updated_at = read_i64(&mut c)?;
        let version = read_u32(&mut c)?;
        const FIXED_PREFIX_LEN: usize = 53;
        let variable_len = title_len + trigger_len;
        if FIXED_PREFIX_LEN + variable_len > data.len() {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "ActionChainSlot variable fields exceed data",
            ));
        }
        let mut title_buf = vec![0u8; title_len];
        c.read_exact(&mut title_buf)?;
        let title = String::from_utf8(title_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
        let mut trigger_buf = vec![0u8; trigger_len];
        c.read_exact(&mut trigger_buf)?;
        let trigger = String::from_utf8(trigger_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
        Ok(ActionChainSlot {
            id_hash,
            title,
            trigger,
            status,
            confidence,
            success_rate,
            trigger_count,
            last_triggered,
            created_at,
            updated_at,
            version,
        })
    }
}

// ============================================================================
// ActionStep — individual step within a chain
// ============================================================================

#[derive(Debug, Clone, PartialEq)]
pub struct ActionStep {
    pub id_hash: u64,
    pub chain_id: u64,
    pub step_order: u16,
    pub action: String,
    pub parameters: Option<String>, // JSON format
    pub created_at: i64,
}

impl ActionStep {
    /// Fixed 30 bytes + variable `action.len() + params.len()` (or 0).
    pub fn slot_size(&self) -> usize {
        30 + self.action.len() + self.parameters.as_ref().map_or(0, |p| p.len())
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());
        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&self.chain_id.to_le_bytes())?;
        buf.write_all(&self.step_order.to_le_bytes())?;
        buf.write_all(&(self.action.len() as u16).to_le_bytes())?;
        let params_len = self.parameters.as_ref().map_or(0u16, |p| p.len() as u16);
        buf.write_all(&params_len.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        buf.write_all(self.action.as_bytes())?;
        if let Some(ref params) = self.parameters {
            buf.write_all(params.as_bytes())?;
        }
        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);
        let id_hash = read_u64(&mut c)?;
        let chain_id = read_u64(&mut c)?;
        let step_order = read_u16(&mut c)?;
        let action_len = read_u16(&mut c)? as usize;
        let params_len = read_u16(&mut c)? as usize;
        let created_at = read_i64(&mut c)?;
        const FIXED_PREFIX_LEN: usize = 30;
        let variable_len = action_len + params_len;
        if FIXED_PREFIX_LEN + variable_len > data.len() {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "ActionStep variable fields exceed data",
            ));
        }
        let mut action_buf = vec![0u8; action_len];
        c.read_exact(&mut action_buf)?;
        let action = String::from_utf8(action_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
        let parameters = if params_len > 0 {
            let mut params_buf = vec![0u8; params_len];
            c.read_exact(&mut params_buf)?;
            Some(
                String::from_utf8(params_buf)
                    .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?,
            )
        } else {
            None
        };
        Ok(ActionStep {
            id_hash,
            chain_id,
            step_order,
            action,
            parameters,
            created_at,
        })
    }
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_action_chain_roundtrip() {
        let chain = ActionChainSlot {
            id_hash: 123456789,
            title: "Deploy Service".into(),
            trigger: "keyword:deploy AND service:production".into(),
            status: ChainStatus::Active,
            confidence: 0.85,
            success_rate: 0.92,
            trigger_count: 5,
            last_triggered: 1000000,
            created_at: 900000,
            updated_at: 950000,
            version: 1,
        };
        let data = chain.serialize().unwrap();
        assert_eq!(data.len(), chain.slot_size());
        assert_eq!(chain, ActionChainSlot::deserialize(&data).unwrap());
    }

    #[test]
    fn test_action_chain_empty_strings() {
        let chain = ActionChainSlot {
            id_hash: 1,
            title: "".into(),
            trigger: "".into(),
            status: ChainStatus::Draft,
            confidence: 0.0,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: 0,
            updated_at: 0,
            version: 0,
        };
        assert_eq!(chain.serialize().unwrap().len(), 53);
        assert_eq!(
            chain,
            ActionChainSlot::deserialize(&chain.serialize().unwrap()).unwrap()
        );
    }

    #[test]
    fn test_action_chain_all_statuses() {
        for status in [
            ChainStatus::Draft,
            ChainStatus::Active,
            ChainStatus::Deprecated,
        ] {
            let chain = ActionChainSlot {
                id_hash: 1,
                title: "test".into(),
                trigger: "always".into(),
                status,
                confidence: 1.0,
                success_rate: 1.0,
                trigger_count: 1,
                last_triggered: 100,
                created_at: 0,
                updated_at: 0,
                version: 0,
            };
            assert_eq!(
                ActionChainSlot::deserialize(&chain.serialize().unwrap())
                    .unwrap()
                    .status,
                status
            );
        }
    }

    #[test]
    fn test_action_step_roundtrip() {
        let step = ActionStep {
            id_hash: 1,
            chain_id: 100,
            step_order: 1,
            action: "search".into(),
            parameters: Some(r#"{"query":"Rust docs"}"#.into()),
            created_at: 1000,
        };
        assert_eq!(
            step,
            ActionStep::deserialize(&step.serialize().unwrap()).unwrap()
        );
    }

    #[test]
    fn test_action_step_no_params() {
        let step = ActionStep {
            id_hash: 2,
            chain_id: 100,
            step_order: 2,
            action: "summarize".into(),
            parameters: None,
            created_at: 2000,
        };
        let restored = ActionStep::deserialize(&step.serialize().unwrap()).unwrap();
        assert_eq!(step, restored);
        assert_eq!(restored.parameters, None);
    }

    #[test]
    fn test_action_step_slot_size() {
        let step = ActionStep {
            id_hash: 1,
            chain_id: 1,
            step_order: 0,
            action: "act".into(),
            parameters: Some("{}".into()),
            created_at: 0,
        };
        assert_eq!(step.slot_size(), 35); // 30 + 3 + 2
    }

    #[test]
    fn test_action_chain_has_updated_at() {
        let chain = ActionChainSlot {
            id_hash: 1,
            title: "test".into(),
            trigger: "t".into(),
            status: ChainStatus::Active,
            confidence: 0.5,
            success_rate: 0.8,
            trigger_count: 3,
            last_triggered: 500,
            created_at: 100,
            updated_at: 600,
            version: 2,
        };
        let r = ActionChainSlot::deserialize(&chain.serialize().unwrap()).unwrap();
        assert_eq!(r.updated_at, 600);
        assert_eq!(r.success_rate, 0.8);
    }
}
