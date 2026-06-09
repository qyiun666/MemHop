//! MemHop SDK — 进程内共享编码器 + 简化初始化
//!
//! # 设计目标
//! - 向量模型路径可配置（不硬编码）
//! - 多个 Brain 实例共享同一个向量编码器（节省内存）
//! - 懒加载：第一次使用时才加载模型
//!
//! # 使用示例
//! ```rust,no_run
//! use memhop_core::{MemHopConfig, MemHopSDK};
//!
//! // 1. 初始化 SDK（全局一次性）
//! let config = MemHopConfig {
//!     model_path: Some("/path/to/multilingual-e5-small".to_string()),
//!     ..Default::default()
//! };
//! MemHopSDK::init(config).unwrap();
//!
//! // 2. 创建 Brain 实例（自动使用共享编码器）
//! let brain = MemHopSDK::create_brain("./data/agent1", "agent1").unwrap();
//! ```

use std::sync::{Arc, OnceLock, RwLock};

use crate::encoder::{Encoder, NgramEncoder};
use crate::error::{MemHopError, Result};
use crate::types::BrainConfig;
use crate::Brain;

/// SDK 配置
#[derive(Debug, Clone, Default)]
pub struct MemHopConfig {
    /// 向量模型路径（可选）
    /// - `Some(path)`: 使用 CandleEncoder + EncoderRouter 双通道
    /// - `None`: 仅使用 NgramEncoder（BM25 稀疏检索）
    pub model_path: Option<String>,

    /// 向量维度（默认 384，multilingual-e5-small）
    /// 仅在 model_path 为 Some 时生效
    pub vector_dim: usize,

    /// 是否启用 candle feature（需要编译时启用）
    #[cfg(feature = "candle")]
    pub use_candle: bool,
}

impl MemHopConfig {
    /// 从环境变量或配置文件加载
    pub fn from_env() -> Self {
        let model_path = std::env::var("MEMHOP_MODEL_PATH").ok();
        Self {
            model_path,
            vector_dim: 384,
            #[cfg(feature = "candle")]
            use_candle: true,
        }
    }
}

/// MemHop SDK 入口
///
/// 使用全局单例模式，确保同一进程内所有 Brain 实例共享同一个编码器。
pub struct MemHopSDK;

/// 全局编码器实例（进程级单例）
static GLOBAL_ENCODER: OnceLock<Arc<Box<dyn Encoder>>> = OnceLock::new();
/// SDK 初始化状态
static SDK_STATE: OnceLock<RwLock<Option<MemHopConfig>>> = OnceLock::new();

impl MemHopSDK {
    /// 初始化 SDK（全局一次性调用）
    ///
    /// # 重要
    /// - 必须在任何 Brain 创建之前调用
    /// - 多次调用会被忽略（返回 Ok）
    /// - 如果已初始化，新配置会被忽略
    pub fn init(config: MemHopConfig) -> Result<()> {
        let state = SDK_STATE.get_or_init(|| RwLock::new(None));

        // 检查是否已初始化
        if let Ok(guard) = state.read()
            && guard.is_some() {
                return Ok(()); // 已初始化，忽略重复调用
        }

        // 创建编码器
        let encoder = Self::create_encoder(&config)?;

        // 存储全局状态
        GLOBAL_ENCODER.set(encoder).map_err(|_| {
            MemHopError::Internal("Failed to set global encoder".to_string())
        })?;

        if let Ok(mut guard) = state.write() {
            *guard = Some(config);
        }

        Ok(())
    }

    /// 创建编码器（内部方法，供 MemHopInstance 复用）
    pub(crate) fn create_encoder(config: &MemHopConfig) -> Result<Arc<Box<dyn Encoder>>> {
        match &config.model_path {
            Some(model_path) => {
                #[cfg(feature = "candle")]
                {
                    // 使用 CandleEncoder + EncoderRouter
                    use crate::encoder::EncoderRouter;
                    match crate::CandleEncoder::new(model_path) {
                        Ok(dense_encoder) => {
                            eprintln!("[MemHopSDK] Loaded CandleEncoder from: {}", model_path);
                            let sparse = Box::new(NgramEncoder::new(config.vector_dim));
                            let dense = Box::new(dense_encoder);
                            let router = EncoderRouter::new(sparse, dense);
                            Ok(Arc::new(Box::new(router)))
                        }
                        Err(e) => {
                            eprintln!("[MemHopSDK] Failed to load CandleEncoder: {}, falling back to NgramEncoder", e);
                            Ok(Arc::new(Box::new(NgramEncoder::new(config.vector_dim))))
                        }
                    }
                }
                #[cfg(not(feature = "candle"))]
                {
                    let _ = model_path; // suppress unused warning
                    eprintln!("[MemHopSDK] candle feature not enabled, using NgramEncoder only");
                    Ok(Arc::new(Box::new(NgramEncoder::new(config.vector_dim))))
                }
            }
            None => {
                // 仅使用 NgramEncoder
                Ok(Arc::new(Box::new(NgramEncoder::new(1024))))
            }
        }
    }

    /// 获取全局编码器
    pub fn get_encoder() -> Result<Arc<Box<dyn Encoder>>> {
        GLOBAL_ENCODER
            .get()
            .cloned()
            .ok_or_else(|| MemHopError::Internal("MemHopSDK not initialized. Call MemHopSDK::init() first.".to_string()))
    }

    /// 创建 Brain 实例（自动使用全局编码器）
    pub fn create_brain(brains_dir: &str, agent_id: &str) -> Result<Brain> {
        let encoder = Self::get_encoder()?;
        let config = BrainConfig {
            brains_dir: brains_dir.to_string(),
            agent_id: agent_id.to_string(),
        };
        Brain::open(config, encoder)
    }

    /// 检查 SDK 是否已初始化
    pub fn is_initialized() -> bool {
        GLOBAL_ENCODER.get().is_some()
    }

    /// 获取当前配置（如果已初始化）
    pub fn get_config() -> Option<MemHopConfig> {
        SDK_STATE
            .get()
            .and_then(|state| state.read().ok())
            .and_then(|guard| guard.clone())
    }
}

/// Non-global MemHop instance (for testing or multi-config scenarios).
///
/// Unlike `MemHopSDK` which uses process-wide singletons, `MemHopInstance`
/// holds its own encoder and config. This enables:
/// - Parallel tests with different configurations
/// - Multiple Brain instances with different encoders
/// - Testing initialization failure scenarios
pub struct MemHopInstance {
    encoder: Arc<Box<dyn Encoder>>,
    config: MemHopConfig,
}

impl MemHopInstance {
    /// Create a new MemHopInstance with the given config.
    /// This does not affect the global SDK state.
    pub fn new(config: MemHopConfig) -> Result<Self> {
        let encoder = MemHopSDK::create_encoder(&config)?;
        Ok(Self { encoder, config })
    }

    /// Create a Brain instance using this instance's encoder.
    pub fn create_brain(&self, brains_dir: &str, agent_id: &str) -> Result<Brain> {
        let brain_config = BrainConfig {
            brains_dir: brains_dir.to_string(),
            agent_id: agent_id.to_string(),
        };
        Brain::open(brain_config, self.encoder.clone())
    }

    /// Get a reference to this instance's encoder.
    pub fn encoder(&self) -> Arc<Box<dyn Encoder>> {
        self.encoder.clone()
    }

    /// Get the config used to create this instance.
    pub fn config(&self) -> &MemHopConfig {
        &self.config
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_sdk_init_default() {
        let config = MemHopConfig::default();
        // 由于 OnceLock，多次初始化不会失败
        let result = MemHopSDK::init(config);
        assert!(result.is_ok());
    }

    #[test]
    fn test_sdk_create_brain() {
        let tmp = tempfile::tempdir().unwrap();
        let config = MemHopConfig::default();
        let _ = MemHopSDK::init(config);

        let brain = MemHopSDK::create_brain(
            tmp.path().to_str().unwrap(),
            "test-agent"
        );
        assert!(brain.is_ok());
    }

    #[test]
    fn test_sdk_is_initialized() {
        // 初始化 SDK
        let config = MemHopConfig::default();
        let _ = MemHopSDK::init(config);
        // 检查是否已初始化
        assert!(MemHopSDK::is_initialized());
    }

    #[test]
    fn test_instance_new_default() {
        let config = MemHopConfig::default();
        let instance = MemHopInstance::new(config).unwrap();
        // Verify encoder is created and usable
        let _encoder = instance.encoder();
        // Verify config is accessible
        let _cfg = instance.config();
    }

    #[test]
    fn test_instance_create_brain() {
        let tmp = tempfile::tempdir().unwrap();
        let config = MemHopConfig::default();
        let instance = MemHopInstance::new(config).unwrap();

        let brain = instance.create_brain(
            tmp.path().to_str().unwrap(),
            "test-instance-agent"
        );
        assert!(brain.is_ok());
    }

    #[test]
    fn test_instance_config() {
        let config = MemHopConfig {
            model_path: None,
            vector_dim: 512,
            #[cfg(feature = "candle")]
            use_candle: false,
        };
        let instance = MemHopInstance::new(config.clone()).unwrap();
        assert_eq!(instance.config().vector_dim, 512);
        assert_eq!(instance.config().model_path, None);
    }
}
