// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

#[cfg(feature = "grpc-encoder")]
pub mod grpc;

#[cfg(feature = "grpc-encoder")]
pub use grpc::{Encoder, EncoderOutput, GrpcEncoder, DEFAULT_ENCODER_ADDR};
