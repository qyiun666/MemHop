// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Search pipeline: decomposed search_context steps as reusable methods.
//!
//! Each step is independently callable so other APIs (update_memory, import, etc.)
//! can reuse retrieval, association, and assembly logic without coupling to the
//! full search_context orchestration.

// Pipeline modules are only compiled when grpc-encoder is enabled,
// matching the feature gate on search_context.
#[cfg(feature = "grpc-encoder")]
pub(crate) mod assemble;
#[cfg(feature = "grpc-encoder")]
#[cfg(feature = "grpc-encoder")]
pub(crate) mod l1_assoc;
#[cfg(feature = "grpc-encoder")]
pub(crate) mod l2_search;
#[cfg(feature = "grpc-encoder")]
pub(crate) mod l3_import;
#[cfg(feature = "grpc-encoder")]
pub(crate) mod optimize;
