use crate::brain::Brain;
use crate::error::{MemHopError, Result};
use crate::types::ProceduralCrystal;

impl Brain {
    /// v0.18.3: 存储一个程序性晶体。
    pub fn store_crystal(&mut self, crystal: &ProceduralCrystal) -> Result<()> {
        match self.redb_store.as_ref() {
            Some(store) => store.l5_store_crystal(crystal),
            None => Err(MemHopError::Storage("redb not available".into())),
        }
    }

    /// v0.18.3: 按 ID 获取程序性晶体。
    pub fn get_crystal(&mut self, id: &str) -> Result<Option<ProceduralCrystal>> {
        match self.redb_store.as_ref() {
            Some(store) => store.l5_get_crystal(id),
            None => Ok(None),
        }
    }

    /// v0.18.3: 列出所有程序性晶体。
    pub fn list_crystals(&mut self) -> Result<Vec<ProceduralCrystal>> {
        match self.redb_store.as_ref() {
            Some(store) => store.l5_list_crystals(),
            None => Ok(Vec::new()),
        }
    }

    /// v0.18.3: 按关键词过滤程序性晶体（子串匹配 trigger_keywords）。
    pub fn get_crystals_by_keyword(&mut self, keyword: &str) -> Result<Vec<ProceduralCrystal>> {
        match self.redb_store.as_ref() {
            Some(store) => store.l5_get_crystals_by_keyword(keyword),
            None => Ok(Vec::new()),
        }
    }

    /// v0.18.3: 更新程序性晶体的使用反馈。
    /// success=true 表示晶体被成功采用，false 表示被拒绝。
    pub fn update_crystal_usage(&mut self, crystal_id: &str, success: bool) -> Result<()> {
        let mut crystal = self.get_crystal(crystal_id)?
            .ok_or_else(|| MemHopError::NotFound(format!("crystal {} not found", crystal_id)))?;
        crystal.usage_count += 1;
        // 指数移动平均更新成功率
        let alpha = 0.1f32;
        crystal.success_rate = alpha * (if success { 1.0 } else { 0.0 })
            + (1.0 - alpha) * crystal.success_rate;
        crystal.updated_at = chrono::Utc::now().timestamp_millis();
        self.store_crystal(&crystal)
    }
}
