// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Convenient re-exports of the public MemHop API.

#![allow(unused_imports)]

pub use crate::api::MemHop;
pub use crate::api::{MemHopError, Result};
pub use crate::config::*;
pub use crate::dream::llm::{CrystalDef, CrystalStep, LlmProvider, MemorySummary, Pattern};
#[cfg(feature = "llm")]
pub use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
pub use crate::dream::prune::DreamReport;
#[cfg(feature = "grpc-encoder")]
pub use crate::encoder::{Encoder, EncoderOutput};
pub use crate::query::batch::{BatchReport, EncodedItem, StoreBatch, StoreItem};
pub use crate::query::types::*;
pub use crate::util::{Layer, SourceMeta, SourceRef, SourceType};
