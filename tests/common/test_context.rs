use memhop::{MemHop, MemHopConfig};
use std::path::PathBuf;
use std::fs;

pub struct TestContext {
    pub db: MemHop,
    pub db_path: PathBuf,
    pub socket_path: PathBuf,

    // 累积的测试数据 ID
    pub created_l2_ids: Vec<String>,
    #[allow(dead_code)]
    pub created_l3_ids: Vec<String>,
    #[allow(dead_code)]
    pub created_l5_ids: Vec<String>,
}

impl TestContext {
    pub fn setup() -> Self {
        // 1. 确定路径
        let db_path = PathBuf::from("/tmp/memhop_e2e_test.meh");
        let socket_path = PathBuf::from("/tmp/memhop_encoder_e2e.sock");

        // 清理旧数据
        let _ = fs::remove_file(&db_path);

        // 2. 创建 MemHop 实例（使用 MockEncoder，因为真实模型加载太慢）
        let config = MemHopConfig {
            db_path: db_path.clone(),
            encoder_socket: socket_path.clone(),
            vector_dim: 384,  // multilingual-e5-small 实际维度
            crystal_path: None,
        };
        let db = MemHop::open(config).expect("Failed to open test database");

        // 3. 返回上下文
        TestContext {
            db,
            db_path,
            socket_path,
            created_l2_ids: Vec::new(),
            created_l3_ids: Vec::new(),
            created_l5_ids: Vec::new(),
        }
    }
}

impl Drop for TestContext {
    fn drop(&mut self) {
        // MemHop 的 Drop 实现会自动调用 checkpoint()
        // 清理数据库文件
        let _ = fs::remove_file(&self.db_path);
    }
}
