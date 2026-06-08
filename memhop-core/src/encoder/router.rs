use super::{Encoder, EncoderOutput};

/// EncoderRouter - 双编码器路由
///
/// 合并 NgramEncoder (sparse) + CandleEncoder (dense) 输出
/// - sparse: 来自 NgramEncoder，用于 BM25 稀疏检索
/// - dense: 来自 CandleEncoder，用于 HNSW 稠密向量检索
///
/// 当仅有 NgramEncoder 时，使用 ngram_only() 退化模式
pub struct EncoderRouter {
    sparse_encoder: Box<dyn Encoder>,
    dense_encoder: Option<Box<dyn Encoder>>,
}

impl EncoderRouter {
    /// 构造双编码器路由
    ///
    /// # Arguments
    /// * `sparse_encoder` - 稀疏编码器（通常是 NgramEncoder）
    /// * `dense_encoder` - 稠密编码器（通常是 CandleEncoder）
    pub fn new(sparse_encoder: Box<dyn Encoder>, dense_encoder: Box<dyn Encoder>) -> Self {
        Self {
            sparse_encoder,
            dense_encoder: Some(dense_encoder),
        }
    }

    /// 退化模式：仅有 sparse 编码器
    ///
    /// 当没有 CandleEncoder 时，使用 NgramEncoder 同时生成 sparse 和 dense
    /// dense 由 NgramEncoder 的 FNV-1a 哈希生成（1024维）
    pub fn ngram_only(encoder: Box<dyn Encoder>) -> Self {
        Self {
            sparse_encoder: encoder,
            dense_encoder: None,
        }
    }

    /// 获取 sparse 编码器引用（用于测试）
    pub fn sparse_encoder(&self) -> &dyn Encoder {
        self.sparse_encoder.as_ref()
    }

    /// 获取 dense 编码器引用（用于测试）
    pub fn dense_encoder(&self) -> Option<&dyn Encoder> {
        self.dense_encoder.as_ref().map(|e| e.as_ref())
    }
}

impl Encoder for EncoderRouter {
    fn encode(&self, text: &str) -> EncoderOutput {
        // 获取 sparse 输出（总是来自 sparse_encoder）
        let sparse_output = self.sparse_encoder.encode(text);

        // 获取 dense 输出
        let dense = if let Some(ref dense_encoder) = self.dense_encoder {
            // 双编码器模式：dense 来自 dense_encoder
            dense_encoder.encode(text).dense
        } else {
            // ngram_only 模式：dense 来自 sparse_encoder 的完整输出
            sparse_output.dense.clone()
        };

        // 合并：sparse 来自 sparse_encoder，dense 来自 dense_encoder 或 sparse_encoder
        EncoderOutput {
            dense,
            sparse: sparse_output.sparse,
        }
    }

    fn dim(&self) -> usize {
        // HNSW 维度由 dense_encoder 决定（如果有），否则由 sparse_encoder 决定
        if let Some(ref dense_encoder) = self.dense_encoder {
            dense_encoder.dim()
        } else {
            self.sparse_encoder.dim()
        }
    }

    fn mode(&self) -> &str {
        "router"
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::encoder::NgramEncoder;

    #[test]
    fn test_ngram_only_mode() {
        let ngram = NgramEncoder::new(1024);
        let router = EncoderRouter::ngram_only(Box::new(ngram));

        assert_eq!(router.dim(), 1024);
        assert_eq!(router.mode(), "router");

        let output = router.encode("test query");
        // ngram_only 模式下，sparse 和 dense 都应有内容
        assert!(!output.sparse.is_empty());
        assert_eq!(output.dense.len(), 1024);
    }

    #[test]
    fn test_router_dim_from_sparse() {
        let ngram = NgramEncoder::new(1024);
        let router = EncoderRouter::ngram_only(Box::new(ngram));

        // ngram_only 模式下，dim 应来自 sparse_encoder
        assert_eq!(router.dim(), 1024);
        assert!(router.dense_encoder().is_none());
    }

    #[test]
    fn test_router_encode_combines_outputs() {
        let ngram = NgramEncoder::new(1024);
        let router = EncoderRouter::ngram_only(Box::new(ngram));

        let output = router.encode("hello world");

        // sparse 应包含 ngram 特征
        assert!(!output.sparse.is_empty());

        // dense 应有 1024 维向量
        assert_eq!(output.dense.len(), 1024);

        // 验证 dense 是 L2 normalized
        let norm: f32 = output.dense.iter().map(|x| x.to_f32().powi(2)).sum::<f32>().sqrt();
        assert!((norm - 1.0).abs() < 0.01, "L2 norm should be close to 1.0, got {}", norm);
    }
}
