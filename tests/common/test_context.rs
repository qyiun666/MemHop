use super::test_encoder::TestEncoder;
use memhop::{MemHop, MemHopConfig};
use std::fs;
use std::path::PathBuf;

pub struct TestContext {
    pub db: MemHop,
    pub db_path: PathBuf,

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

        // 清理旧数据
        let _ = fs::remove_file(&db_path);

        // 2. 创建 MemHop 实例（无真实编码器，使用 TestEncoder）
        let config = MemHopConfig {
            db_path: db_path.clone(),
            encoder_grpc_addr: None,
            vector_dim: 384,
            crystal_path: None,
        };
        let mut db = MemHop::open(config).expect("Failed to open test database");

        // 注入测试编码器
        db.set_encoder(TestEncoder::new(384));

        // 3. 返回上下文
        TestContext {
            db,
            db_path,
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
