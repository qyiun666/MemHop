use crate::brain::Brain;
use crate::engram::KnowledgeNode;
use crate::error::{MemHopError, Result};
use crate::recall::build_prefetch_hints;
use crate::types::{
    EmotionalDimension, EmotionalFeedback, Emotion, EmotionRecallRequest, RecallResponse,
    RecallResult,
};
use std::collections::HashSet;

impl Brain {
    /// 获取记忆的情感维度
    pub fn get_emotion(&mut self, memory_id: &str) -> Result<EmotionalDimension> {
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let node = store.l1_get_node(memory_id)?
            .ok_or_else(|| MemHopError::NotFound(format!("emotion not found for memory {}", memory_id)))?;
        Ok(EmotionalDimension {
            emotion: node.memory.emotion,
            intensity: node.memory.emotion_intensity,
            valence: node.memory.valence,
            arousal: node.memory.arousal,
        })
    }

    /// 情感反馈 — 根据用户情感调节记忆 importance。
    pub fn emotional_feedback(&mut self, feedback: &EmotionalFeedback) -> Result<()> {
        feedback.validate()?;
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;

        let mut node = match store.l1_get_node(&feedback.memory_id)? {
            Some(n) => n,
            None => return Err(MemHopError::NotFound(format!("memory {}", feedback.memory_id))),
        };

        let delta = match feedback.emotion {
            Emotion::Joy => 0.05,
            Emotion::Sadness => -0.03,
            Emotion::Anger => 0.02,
            Emotion::Fear => 0.04,
            Emotion::Surprise => 0.06,
            Emotion::Disgust => -0.02,
            Emotion::Neutral => 0.0,
        };
        node.memory.importance = (node.memory.importance + delta * feedback.intensity).clamp(0.0, 1.0);
        node.memory.emotion = feedback.emotion;
        node.memory.emotion_intensity = feedback.intensity;
        node.updated_at = chrono::Utc::now().timestamp_millis();
        store.l1_store_node(&node)?;
        Ok(())
    }

    /// 按情感检索记忆
    pub fn recall_by_emotion(&mut self, req: &EmotionRecallRequest) -> Result<RecallResponse> {
        self.ensure_l1()?;
        let store = self.redb_store
            .as_ref()
            .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
        let rtxn = store.begin_read()
            .map_err(|e| MemHopError::Storage(format!("begin_read: {}", e)))?;
        let nodes: Vec<(String, KnowledgeNode)> = store
            .iter_bincode(&rtxn, crate::storage::L1_NODES)
            .unwrap_or_default();
        drop(rtxn);

        let mut results: Vec<RecallResult> = Vec::new();
        for (_key, node) in nodes {
            if let Some(target_emotion) = req.emotion
                && node.memory.emotion != target_emotion
            {
                continue;
            }

            if node.memory.emotion_intensity < req.min_intensity {
                continue;
            }

            let hours_since = (chrono::Utc::now().timestamp_millis() - node.created_at) as f32
                / 3_600_000.0;
            let decay = req
                .time_decay_lambda
                .map(|lambda| (-lambda * hours_since).exp())
                .unwrap_or(1.0);
            let score = node.memory.emotion_intensity * decay;

            results.push(crate::types::RecallResult {
                layer: crate::types::Layer::L1,
                id: node.id.clone(),
                text: node.text.clone(),
                score,
                topic_label: None,
                created_at: node.created_at,
                version: node.version,
                emotion: Some(EmotionalDimension {
                    emotion: node.memory.emotion,
                    intensity: node.memory.emotion_intensity,
                    valence: node.memory.valence,
                    arousal: node.memory.arousal,
                }),
                domain_id: None,
                source_ref: None,
                is_structural: false,
                neighbors: Vec::new(),
            });
        }

        results.sort_by(|a, b| b.score.partial_cmp(&a.score).unwrap_or(std::cmp::Ordering::Equal));
        results.truncate(req.max_results);
        let total = results.len();

        // 构建 prefetch 提示
        let seen: HashSet<String> = results.iter().map(|r| r.id.clone()).collect();
        let prefetch = build_prefetch_hints(self, &results, &seen, 5);

        Ok(RecallResponse {
            results,
            total_count: total,
            l0_profile: None,
            confidence: None,
            activated_topics: Vec::new(),
            recommended_crystals: Vec::new(),
            prefetch,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_emotional_feedback_validates() {
        let fb = EmotionalFeedback {
            memory_id: "mem_1".into(),
            emotion: Emotion::Joy,
            intensity: 0.5,
            reason: None,
        };
        assert!(fb.validate().is_ok());
    }

    #[test]
    fn test_emotional_feedback_empty_id_rejected() {
        let fb = EmotionalFeedback {
            memory_id: "".into(),
            emotion: Emotion::Joy,
            intensity: 0.5,
            reason: None,
        };
        assert!(fb.validate().is_err());
    }

    #[test]
    fn test_emotional_feedback_nan_intensity_rejected() {
        let fb = EmotionalFeedback {
            memory_id: "mem_1".into(),
            emotion: Emotion::Joy,
            intensity: f32::NAN,
            reason: None,
        };
        assert!(fb.validate().is_err());
    }

    #[test]
    fn test_emotion_recall_request_validate_ok() {
        let req = EmotionRecallRequest {
            emotion: Some(Emotion::Joy),
            min_intensity: 0.3,
            time_decay_lambda: Some(0.001),
            max_results: 50,
        };
        assert!(req.validate().is_ok());
    }

    #[test]
    fn test_emotion_recall_request_max_results_too_high_rejected() {
        let req = EmotionRecallRequest {
            emotion: None,
            min_intensity: 0.0,
            time_decay_lambda: None,
            max_results: 99999,
        };
        assert!(req.validate().is_err());
    }
}
