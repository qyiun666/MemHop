use std::fmt;

#[derive(Debug)]
pub enum MemHopError {
    Storage(String),
    StorageFull(String),
    Encode(String),
    NotFound(String),
    InvalidArgument(String),
    Internal(String),
    Batch(String),
}

impl fmt::Display for MemHopError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            MemHopError::Storage(msg) => write!(f, "storage error: {}", msg),
            MemHopError::StorageFull(msg) => write!(f, "storage full: {}", msg),
            MemHopError::Encode(msg) => write!(f, "encode error: {}", msg),
            MemHopError::NotFound(msg) => write!(f, "not found: {}", msg),
            MemHopError::InvalidArgument(msg) => write!(f, "invalid argument: {}", msg),
            MemHopError::Internal(msg) => write!(f, "internal error: {}", msg),
            MemHopError::Batch(msg) => write!(f, "batch error: {}", msg),
        }
    }
}

impl std::error::Error for MemHopError {}

pub type Result<T> = std::result::Result<T, MemHopError>;

/// 对内部错误消息进行脱敏分类，防止 LMDB/bincode 实现细节泄漏。
///
/// 分类规则（基于错误消息内容）：
/// - 包含 "not found" → `MemHopError::NotFound`（保留 ID/资源信息）
/// - 包含 "invalid" 或 "must be" → `MemHopError::InvalidArgument`（保留字段信息）
/// - 其他 → `MemHopError::Internal`（替换为泛化描述）
pub fn sanitize_error(context: &str, msg: &str) -> MemHopError {
    let lower = msg.to_lowercase();
    if lower.contains("not found") {
        MemHopError::NotFound(msg.to_string())
    } else if lower.contains("invalid") || lower.contains("must be") {
        MemHopError::InvalidArgument(msg.to_string())
    } else {
        MemHopError::Internal(format!("internal memory error: {}", context))
    }
}

impl From<serde_json::Error> for MemHopError {
    fn from(e: serde_json::Error) -> Self {
        sanitize_error("json", &e.to_string())
    }
}

impl From<bincode::Error> for MemHopError {
    fn from(e: bincode::Error) -> Self {
        sanitize_error("bincode", &e.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_sanitize_not_found_preserves_id() {
        let err = sanitize_error("get emotion", "emotion not found: mem_001");
        assert!(matches!(err, MemHopError::NotFound(_)));
        assert!(err.to_string().contains("mem_001"), "应保留 ID 信息");
    }

    #[test]
    fn test_sanitize_heed_database_not_found() {
        let err = sanitize_error("open db", "database not found");
        assert!(matches!(err, MemHopError::NotFound(_)));
    }

    #[test]
    fn test_sanitize_invalid_argument_preserves_field_info() {
        let err = sanitize_error("validate", "invalid argument: intensity must be in [0.0, 1.0]");
        assert!(matches!(err, MemHopError::InvalidArgument(_)));
        assert!(err.to_string().contains("intensity"), "应保留字段信息");
    }

    #[test]
    fn test_sanitize_must_be_pattern() {
        let err = sanitize_error("validate", "intensity must be >= 0.0");
        assert!(matches!(err, MemHopError::InvalidArgument(_)));
        assert!(err.to_string().contains("intensity"));
    }

    #[test]
    fn test_sanitize_internal_not_expose_lmdb_path() {
        // LMDB 错误消息通常包含路径信息
        let lmdb_err = "MDB_PANIC: /var/lib/memhop/data/lmdb: Environment panic";
        let err = sanitize_error("lmdb write", lmdb_err);
        assert!(matches!(err, MemHopError::Internal(_)));
        let msg = err.to_string();
        assert!(!msg.contains("/var/lib/memhop/"), "不应包含 LMDB 路径");
        assert!(msg.contains("internal memory error"), "应为泛化描述");
    }

    #[test]
    fn test_sanitize_bincode_internal() {
        let bincode_err = "IoError: No such file or directory";
        let err = sanitize_error("serialize", bincode_err);
        assert!(matches!(err, MemHopError::Internal(_)));
        let msg = err.to_string();
        assert!(!msg.contains("No such file"), "不应包含原始错误细节");
        assert!(msg.contains("internal memory error"), "应为泛化描述");
    }

    #[test]
    fn test_sanitize_generic_internal() {
        let err = sanitize_error("store batch", "some random internal error");
        assert!(matches!(err, MemHopError::Internal(_)));
        let msg = err.to_string();
        assert!(msg.contains("store batch"), "Internal 应保留上下文");
        assert!(!msg.contains("random internal error"), "不应包含原始错误消息");
    }
}

