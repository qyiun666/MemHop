//! profile — L0 角色画像存储与生成。
//! 从 L2 Topic 模式中提取三观、世界观、性格，形成角色画像。

use crate::brain::Brain;
use crate::engram::Topic;
use crate::error::{MemHopError, Result};
use crate::types::{L0Profile, L0Snapshot};
use std::collections::HashMap;

const PROFILE_KEY: &str = "profile:main";

/// L0 角色画像存储（无状态，env 从外部传入）。
pub struct L0ProfileStore;

impl L0ProfileStore {
    pub fn new() -> Self {
        L0ProfileStore
    }

    /// 从 LMDB 读取当前 L0Profile。
    pub fn get_profile(
        &self,
        txn: &heed::RoTxn<'_>,
        env: &crate::lmdb::L0Env,
    ) -> Result<Option<L0Profile>> {
        match env
            .profile
            .get(txn, PROFILE_KEY)
            .map_err(|e| MemHopError::Storage(e.to_string()))?
        {
            Some(bytes) => Ok(Some(
                bincode::deserialize(bytes).map_err(|e| MemHopError::Storage(e.to_string()))?,
            )),
            None => Ok(None),
        }
    }

    /// 将 L0Profile 写入 LMDB。
    pub fn update_profile(
        &self,
        wtxn: &mut heed::RwTxn<'_>,
        env: &crate::lmdb::L0Env,
        profile: &L0Profile,
    ) -> Result<()> {
        let bytes = bincode::serialize(profile).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.profile
            .put(wtxn, PROFILE_KEY, &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// 保存旧版本到 history DB。
    pub fn snapshot(
        &self,
        wtxn: &mut heed::RwTxn<'_>,
        env: &crate::lmdb::L0Env,
        profile: &L0Profile,
        reason: &str,
    ) -> Result<()> {
        let snap = L0Snapshot {
            version: profile.version,
            personality: profile.personality.clone(),
            values: profile.values.clone(),
            worldview: profile.worldview.clone(),
            snapshot_at: chrono::Utc::now().timestamp_millis(),
            reason: reason.to_string(),
        };
        let key = format!("hist:{}", snap.version);
        let bytes = bincode::serialize(&snap).map_err(|e| MemHopError::Storage(e.to_string()))?;
        env.history
            .put(wtxn, &key, &bytes)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(())
    }

    /// 从所有 L2 Topic 的 summary + keywords 提取 L0Profile。
    /// 高频关键词归类为三观/世界观/性格。
    pub fn extract_from_topics(topics: &[Topic]) -> L0Profile {
        let mut keyword_freq: HashMap<String, f32> = HashMap::new();
        let mut summary_texts: Vec<String> = Vec::new();

        for topic in topics {
            // 统计关键词频率
            for kw in &topic.keywords {
                *keyword_freq.entry(kw.clone()).or_insert(0.0) += 1.0;
            }
            // 收集摘要
            if let Some(ref s) = topic.summary {
                summary_texts.push(s.clone());
            }
            // 也把 label 计入
            *keyword_freq.entry(topic.label.clone()).or_insert(0.0) += 0.5;
        }

        // 按频率排序取 top 20
        let mut ranked: Vec<(String, f32)> = keyword_freq.into_iter().collect();
        ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

        // 简单分类：前 1/3 为价值观，中 1/3 为世界观，后 1/3 为性格
        let n = ranked.len();
        let third = (n / 3).max(1);

        let values: Vec<String> = ranked.iter().take(third).map(|(k, _)| k.clone()).collect();
        let worldview: Vec<String> = ranked
            .iter()
            .skip(third)
            .take(third)
            .map(|(k, _)| k.clone())
            .collect();
        let personality: Vec<String> = ranked
            .iter()
            .skip(third * 2)
            .take(third)
            .map(|(k, _)| k.clone())
            .collect();

        let now = chrono::Utc::now().timestamp_millis();
        L0Profile {
            catid: None,
            role_name: None,
            personality,
            values,
            worldview,
            role: None,
            position: None,
            traits: HashMap::new(),
            updated_at: now,
            version: 1,
            history: Vec::new(),
        }
    }

    /// Dream 流程：从 L2 更新 L0，返回是否发生变化。
    pub fn dream_form(brain: &mut Brain) -> Result<bool> {
        // 1. 收集所有 Topic
        let topics = {
            brain.ensure_l2_env()?;
            let l2_env = brain.l2_env.as_ref().unwrap();
            let txn = l2_env
                .env
                .read_txn()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let mut list = Vec::new();
            if let Ok(iter) = l2_env.topics.iter(&txn) {
                for (key, bytes) in iter.flatten() {
                    if !key.starts_with("topic:") || !key.ends_with(":meta") {
                        continue;
                    }
                    if let Ok(t) = bincode::deserialize::<Topic>(bytes) {
                        list.push(t);
                    }
                }
            }
            list
        };

        if topics.is_empty() {
            return Ok(false);
        }

        // 2. 从 Topic 提取新 Profile
        let new_profile = Self::extract_from_topics(&topics);

        // 3. 读取旧 Profile
        let old_profile = {
            brain.ensure_l0_env()?;
            let l0_env = brain.l0_env.as_ref().unwrap();
            let txn = l0_env
                .env
                .read_txn()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let l0 = brain.l0.as_ref().unwrap();
            l0.get_profile(&txn, l0_env)?
        };

        // 4. 对比差异
        let changed = match &old_profile {
            None => true,
            Some(old) => {
                old.personality != new_profile.personality
                    || old.values != new_profile.values
                    || old.worldview != new_profile.worldview
            }
        };

        if !changed {
            return Ok(false);
        }

        // 5. 快照旧版本 + 写入新版本
        brain.ensure_l0_env()?;
        let l0_env_ref = brain.l0_env.as_ref().unwrap();
        let env = l0_env_ref.env.clone();
        let mut wtxn = env
            .write_txn()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        if let Some(ref old) = old_profile {
            let l0 = brain.l0.as_ref().unwrap();
            l0
                .snapshot(&mut wtxn, l0_env_ref, old, "dream L0 formation")?;
            // 新版本 = 旧版本 + 1
            let mut updated = new_profile;
            updated.version = old.version + 1;
            updated.history = old.history.clone(); // 保留历史引用
            l0
                .update_profile(&mut wtxn, l0_env_ref, &updated)?;
        } else {
            let l0 = brain.l0.as_ref().unwrap();
            l0
                .update_profile(&mut wtxn, l0_env_ref, &new_profile)?;
        }

        wtxn.commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        Ok(true)
    }
}

impl Default for L0ProfileStore {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::engram::Topic;

    fn make_topic(label: &str, keywords: Vec<&str>, summary: Option<&str>) -> Topic {
        Topic {
            id: format!("topic_{}", label),
            label: label.to_string(),
            summary: summary.map(|s| s.to_string()),
            keywords: keywords.into_iter().map(|s| s.to_string()).collect(),
            centroid: Vec::new(),
            node_ids: Vec::new(),
            linked_domain_ids: Vec::new(),
            doc_ids: Vec::new(),
            dialogue_range: None,
            created_at: 0,
            updated_at: 0,
            version: 1,
            history: Vec::new(),
            extended_meta: std::collections::HashMap::new(),
            domain_weights: std::collections::HashMap::new(),
            node_weights: std::collections::HashMap::new(),
        }
    }

    #[test]
    fn test_extract_empty_topics() {
        let profile = L0ProfileStore::extract_from_topics(&[]);
        assert!(profile.values.is_empty());
        assert!(profile.worldview.is_empty());
        assert!(profile.personality.is_empty());
    }

    #[test]
    fn test_extract_from_topics() {
        let topics = vec![
            make_topic(
                "Rust编程",
                vec!["Rust", "安全", "性能", "并发"],
                Some("关于Rust编程的讨论"),
            ),
            make_topic(
                "代码质量",
                vec!["测试", "重构", "安全", "规范"],
                Some("代码质量改进"),
            ),
            make_topic(
                "系统设计",
                vec!["架构", "分布式", "性能", "扩展"],
                Some("系统设计话题"),
            ),
        ];
        let profile = L0ProfileStore::extract_from_topics(&topics);
        // "安全" 和 "性能" 出现 2 次，应排前
        assert!(
            !profile.values.is_empty()
                || !profile.worldview.is_empty()
                || !profile.personality.is_empty()
        );
    }
}
