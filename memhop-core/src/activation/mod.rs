//! activation — 记忆激活管理器 (v0.23.0)
//!
//! 实现 Active / Latent / Dormant 三级记忆状态管理。
//! 核心功能：
//! - 计算激活分数（基于重要性和访问时间衰减）
//! - 判断状态转换（基于阈值和重要性保底）
//! - recall 命中后更新激活分数
//! - 个性化衰减系数计算

use crate::types::MemoryState;
use serde::{Deserialize, Serialize};

/// 激活系统配置
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ActivationConfig {
    /// 活跃态容量上限（默认 5000 节点）
    pub active_capacity: usize,
    /// Active → Latent 阈值（默认 0.2）
    pub active_threshold: f32,
    /// Latent → Dormant 阈值（默认 0.05）
    pub dormant_threshold: f32,
    /// 衰减系数 λ（默认 0.001，约 29 天半衰期）
    pub decay_lambda: f32,
    /// recall 命中奖励（默认 0.3）
    pub recall_bonus: f32,
    /// 高于此值不降 Dormant（默认 0.8）
    pub importance_floor_active: f32,
    /// 高于此值不降 Latent（默认 0.95）
    pub importance_floor_latent: f32,
}

impl Default for ActivationConfig {
    fn default() -> Self {
        Self {
            active_capacity: 5000,
            active_threshold: 0.2,
            dormant_threshold: 0.05,
            decay_lambda: 0.001,
            recall_bonus: 0.3,
            importance_floor_active: 0.8,
            importance_floor_latent: 0.95,
        }
    }
}

/// 记忆激活管理器
pub struct ActivationManager {
    config: ActivationConfig,
}

impl ActivationManager {
    /// 创建新的激活管理器
    pub fn new(config: ActivationConfig) -> Self {
        Self { config }
    }

    /// 获取配置的引用
    pub fn config(&self) -> &ActivationConfig {
        &self.config
    }

    /// 计算激活分数
    ///
    /// 公式：score = base_importance × exp(-λ × hours_since_last_access)
    ///
    /// - base_importance: 写入时的初始重要性 [0.0, 1.0]
    /// - hours_since_last_access: 距上次 recall 命中的小时数
    pub fn calculate_score(
        &self,
        base_importance: f32,
        hours_since_last_access: f32,
    ) -> f32 {
        let decay = (-self.config.decay_lambda * hours_since_last_access).exp();
        (base_importance * decay).clamp(0.0, 1.0)
    }

    /// 计算 recall 命中后的激活分数
    ///
    /// 在当前分数基础上加上 recall_bonus，然后 clamp 到 [0.0, 1.0]
    pub fn apply_recall_bonus(&self, current_score: f32) -> f32 {
        (current_score + self.config.recall_bonus).clamp(0.0, 1.0)
    }

    /// 判断应该转换到什么状态
    ///
    /// 规则：
    /// 1. importance >= importance_floor_latent → 始终 Active
    /// 2. importance >= importance_floor_active → 最低 Latent
    /// 3. score >= active_threshold → Active
    /// 4. score >= dormant_threshold → Latent
    /// 5. 其他 → Dormant
    pub fn should_transition(
        &self,
        score: f32,
        importance: f32,
    ) -> MemoryState {
        // 规则 1: 高重要性节点始终 Active
        if importance >= self.config.importance_floor_latent {
            return MemoryState::Active;
        }

        // 规则 2: 较高重要性节点最低 Latent
        if importance >= self.config.importance_floor_active
            && score >= self.config.dormant_threshold
        {
            return MemoryState::Latent;
        }

        // 规则 3-5: 正常状态转换
        if score >= self.config.active_threshold {
            MemoryState::Active
        } else if score >= self.config.dormant_threshold {
            MemoryState::Latent
        } else {
            MemoryState::Dormant
        }
    }

    /// 检查节点是否应该从 Active 降级到 Latent
    pub fn should_demote_from_active(&self, score: f32, importance: f32) -> bool {
        // 高重要性节点不降级
        if importance >= self.config.importance_floor_latent {
            return false;
        }
        score < self.config.active_threshold
    }

    /// 检查节点是否应该从 Latent 降级到 Dormant
    pub fn should_demote_from_latent(&self, score: f32, importance: f32) -> bool {
        // 高重要性节点不降级
        if importance >= self.config.importance_floor_active {
            return false;
        }
        score < self.config.dormant_threshold
    }

    /// 检查节点是否应该从 Latent 升级到 Active
    pub fn should_promote_to_active(&self, score: f32) -> bool {
        score >= self.config.active_threshold
    }

    /// 检查节点是否应该从 Dormant 升级到 Latent
    pub fn should_promote_to_latent(&self, score: f32) -> bool {
        score >= self.config.dormant_threshold
    }
}

/// 计算个性化衰减系数 λ
/// λ = base_λ / (1 + emotional_boost + recall_boost + connectivity_boost)
pub fn personal_decay_lambda(node: &crate::engram::KnowledgeNode, hyperedge_count: usize) -> f32 {
    let base_lambda = 0.01;
    let emotional_boost = node.memory.emotion_intensity * 2.0;
    let recall_boost = node.memory.activation_score * 1.5;
    let connectivity_boost = (hyperedge_count as f32).min(5.0) * 0.3;
    base_lambda / (1.0 + emotional_boost + recall_boost + connectivity_boost)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_calculate_score_no_decay() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // 0 小时后，分数应等于 base_importance
        let score = manager.calculate_score(0.8, 0.0);
        assert!((score - 0.8).abs() < 0.001);
    }

    #[test]
    fn test_calculate_score_with_decay() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // 1000 小时后（约 42 天），分数应显著衰减
        let score = manager.calculate_score(0.8, 1000.0);
        assert!(score < 0.8);
        assert!(score > 0.0);
    }

    #[test]
    fn test_calculate_score_clamp() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // 分数应被 clamp 到 [0.0, 1.0]
        let score_high = manager.calculate_score(1.5, 0.0);
        assert!(score_high <= 1.0);

        let score_low = manager.calculate_score(0.5, 100000.0);
        assert!(score_low >= 0.0);
    }

    #[test]
    fn test_apply_recall_bonus() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // recall 命中后分数应增加 recall_bonus
        let score = manager.apply_recall_bonus(0.5);
        assert!((score - 0.8).abs() < 0.001); // 0.5 + 0.3 = 0.8
    }

    #[test]
    fn test_apply_recall_bonus_clamp() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // recall 命中后分数应被 clamp 到 1.0
        let score = manager.apply_recall_bonus(0.9);
        assert!(score <= 1.0);
    }

    #[test]
    fn test_should_transition_high_importance() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // 高重要性节点始终 Active
        let state = manager.should_transition(0.01, 0.96);
        assert_eq!(state, MemoryState::Active);
    }

    #[test]
    fn test_should_transition_medium_importance() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // 较高重要性节点最低 Latent
        let state = manager.should_transition(0.1, 0.85);
        assert_eq!(state, MemoryState::Latent);
    }

    #[test]
    fn test_should_transition_normal() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // 正常状态转换
        assert_eq!(manager.should_transition(0.5, 0.5), MemoryState::Active);
        assert_eq!(manager.should_transition(0.15, 0.5), MemoryState::Latent);
        assert_eq!(manager.should_transition(0.01, 0.5), MemoryState::Dormant);
    }

    #[test]
    fn test_should_demote_from_active() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // 高分不降级
        assert!(!manager.should_demote_from_active(0.5, 0.5));
        // 低分降级
        assert!(manager.should_demote_from_active(0.1, 0.5));
        // 高重要性不降级
        assert!(!manager.should_demote_from_active(0.1, 0.96));
    }

    #[test]
    fn test_should_demote_from_latent() {
        let manager = ActivationManager::new(ActivationConfig::default());
        // 高分不降级
        assert!(!manager.should_demote_from_latent(0.15, 0.5));
        // 低分降级
        assert!(manager.should_demote_from_latent(0.01, 0.5));
        // 高重要性不降级
        assert!(!manager.should_demote_from_latent(0.01, 0.85));
    }

    #[test]
    fn test_should_promote_to_active() {
        let manager = ActivationManager::new(ActivationConfig::default());
        assert!(manager.should_promote_to_active(0.5));
        assert!(!manager.should_promote_to_active(0.1));
    }

    #[test]
    fn test_should_promote_to_latent() {
        let manager = ActivationManager::new(ActivationConfig::default());
        assert!(manager.should_promote_to_latent(0.1));
        assert!(!manager.should_promote_to_latent(0.01));
    }

    #[test]
    fn test_personal_decay_lambda_high_emotion() {
        let mut node = crate::engram::KnowledgeNode::new(
            "test".into(),
            "test".into(),
            std::collections::HashMap::new(),
            vec![],
            crate::types::Layer::L1,
            crate::types::NodeSource::Perception,
        );
        node.memory.emotion_intensity = 0.8;
        node.memory.activation_score = 0.9;
        let lambda = personal_decay_lambda(&node, 10);
        // 高情感 + 高激活 → λ 应该更小（衰减更慢）
        assert!(lambda < 0.01);
        assert!(lambda > 0.0);
    }

    #[test]
    fn test_personal_decay_lambda_low_emotion() {
        let node = crate::engram::KnowledgeNode::new(
            "test".into(),
            "test".into(),
            std::collections::HashMap::new(),
            vec![],
            crate::types::Layer::L1,
            crate::types::NodeSource::Perception,
        );
        // 默认 emotion_intensity=0.0, activation_score=0.5
        let lambda = personal_decay_lambda(&node, 0);
        // 低情感 + 低连接 → λ 应该接近 base
        assert!((lambda - 0.01).abs() < 0.005);
    }
}
