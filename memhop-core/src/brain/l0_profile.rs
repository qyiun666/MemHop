use crate::brain::Brain;
use crate::error::{MemHopError, Result};
use crate::types::L0Profile;

impl Brain {
    /// 获取 L0 角色画像
    pub fn get_l0_profile(&mut self) -> Result<Option<L0Profile>> {
        if let Some(ref store) = self.redb_store {
            return store.l0_get_profile();
        }
        Ok(None)
    }

    /// 设置 L0 角色画像（身份字段：catid, role_name, role, position, traits）
    /// catid 首次设置后不可修改
    pub fn set_l0_profile(
        &mut self,
        catid: Option<String>,
        role_name: Option<String>,
        role: Option<String>,
        position: Option<String>,
        traits: std::collections::HashMap<String, String>,
    ) -> Result<()> {
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let mut profile = store.l0_get_profile()?.unwrap_or_default();
        // catid 只在首次设置时保存，之后不可修改
        if profile.catid.is_none() && let Some(id) = catid {
            profile.catid = Some(id);
        }
        if let Some(name) = role_name {
            profile.role_name = Some(name);
        }
        if let Some(r) = role {
            profile.role = Some(r);
        }
        if let Some(p) = position {
            profile.position = Some(p);
        }
        if !traits.is_empty() {
            profile.traits.extend(traits);
        }
        profile.version += 1;
        profile.updated_at = chrono::Utc::now().timestamp_millis();
        store.l0_set_profile(&profile)
    }

    /// v0.17.0: LLM 直接写入完整的 L0 角色画像（替代 old set_l0_profile 的逐个字段）。
    /// catid 首次设置后不可修改
    pub fn set_l0(
        &mut self,
        catid: Option<String>,
        role_name: Option<String>,
        personality: Vec<String>,
        values: Vec<String>,
        worldview: Vec<String>,
        traits: std::collections::HashMap<String, String>,
    ) -> Result<()> {
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let mut profile = store.l0_get_profile()?.unwrap_or_default();
        // catid 只在首次设置时保存，之后不可修改
        if profile.catid.is_none() && let Some(id) = catid {
            profile.catid = Some(id);
        }
        if let Some(name) = role_name {
            profile.role_name = Some(name);
        }
        if !personality.is_empty() {
            profile.personality = personality;
        }
        if !values.is_empty() {
            profile.values = values;
        }
        if !worldview.is_empty() {
            profile.worldview = worldview;
        }
        if !traits.is_empty() {
            profile.traits = traits;
        }
        profile.updated_at = chrono::Utc::now().timestamp_millis();
        profile.version += 1;
        store.l0_set_profile(&profile)
    }

    /// 通过 L0Profile 结构体设置角色画像（完整写入所有字段）
    pub fn set_l0_from_profile(&mut self, profile: &L0Profile) -> Result<()> {
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let mut existing = store.l0_get_profile()?.unwrap_or_default();
        // catid 首次设置后不可修改
        if existing.catid.is_none() && profile.catid.is_some() {
            existing.catid = profile.catid.clone();
        }
        existing.role_name = profile.role_name.clone();
        existing.role = profile.role.clone();
        existing.position = profile.position.clone();
        existing.personality = profile.personality.clone();
        existing.values = profile.values.clone();
        existing.worldview = profile.worldview.clone();
        existing.traits = profile.traits.clone();
        existing.version += 1;
        existing.updated_at = chrono::Utc::now().timestamp_millis();
        store.l0_set_profile(&existing)
    }
}
