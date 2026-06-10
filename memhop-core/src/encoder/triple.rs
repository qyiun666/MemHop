use crate::encoder::Encoder;
use half::f16;
use std::collections::HashMap;

/// 三重编码器输出 — 携带三通道所需的所有向量
#[derive(Debug, Clone, Default)]
pub struct TripleEncoderOutput {
    /// NgramEncoder 的稀疏向量（用于 BM25）
    pub sparse: HashMap<String, f32>,
    /// NgramEncoder 的稠密向量（用于 HNSW 通道 2）
    pub dense: Vec<f16>,
    /// E5 模型的稠密向量（用于 HNSW 通道 3，通过 IPC 获得）
    pub dense_e5: Vec<f16>,
}

/// 三重编码器 — 本地 NgramEncoder + 远程 E5 IPC
pub struct TripleEncoder {
    /// 本地 NgramEncoder（始终可用）
    pub local: Box<dyn Encoder>,
    /// 远程 E5 EncoderClient（通过 Unix Socket IPC，可选）
    pub remote: Option<Box<dyn Encoder>>,
}

impl TripleEncoder {
    pub fn new(local: Box<dyn Encoder>, remote: Option<Box<dyn Encoder>>) -> Self {
        TripleEncoder { local, remote }
    }

    /// 三重编码
    pub fn encode(&self, text: &str) -> TripleEncoderOutput {
        let local_output = self.local.encode(text);
        let e5_output = self.remote.as_ref().map(|r| r.encode(text));

        TripleEncoderOutput {
            sparse: local_output.sparse,
            dense: local_output.dense,
            dense_e5: e5_output.map(|o| o.dense).unwrap_or_default(),
        }
    }

    /// E5 通道是否可用
    pub fn e5_available(&self) -> bool {
        self.remote.is_some()
    }

    /// 本地 NgramEncoder 的维度
    pub fn dim(&self) -> usize {
        self.local.dim()
    }

    /// E5 模型的维度（如果可用）
    pub fn e5_dim(&self) -> Option<usize> {
        self.remote.as_ref().map(|r| r.dim())
    }
}
