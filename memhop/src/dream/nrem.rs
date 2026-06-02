//! NREM (Non-Rapid Eye Movement) 阶段 — 衰减、遗忘、矛盾检测

use crate::brain::Brain;
use crate::engram::{AssociationKind, EngramKind, Protection};
use crate::error::{MemHopError, Result};
use crate::types::DreamReport;
use crate::vitality;

// ── NREM-1: Vitality decay ────────────────────────────

/// 扫描 Hippocampus 中的记忆，计算时间衰减后的 vitality。
/// vitality < 0.01 → 删除（遗忘）
/// vitality < 0.1  → 标记 is_archived（归档）
/// 其余 → 正常衰减更新 vitality
pub(crate) fn nrem_vitality_decay(brain: &mut Brain, report: &mut DreamReport) -> Result<()> {
    let entries = brain.hippocampus.all_entries(&brain.storage)?;

    // ── Reconsolidation: 处理 recall buffer ───────────────
    let recalled: Vec<String> = brain.recalled_buffer.borrow_mut().drain(..).collect();
    if !recalled.is_empty() {
        for recalled_id in &recalled {
            if let Some(engram) = entries.iter().find(|(id, _)| id == recalled_id).map(|(_, e)| e.clone()) {
                if engram.protection == Protection::Permanent {
                    continue;
                }
                let mut e = engram;
                vitality::reconsolidate(&mut e.vitality, &mut e.activation_count, &mut e.last_activated);
                let mut txn = brain.storage.begin_write()?;
                brain.storage.put_hippocampus(&mut txn, recalled_id, &e)?;
                txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
            }
        }
    }

    if entries.is_empty() {
        return Ok(());
    }

    // ── Vitality 衰减 ────────────────────────────────────
    let now = super::now_millis();
    let mut decayed = 0u64;
    let mut archived = 0u64;
    let mut forgotten = 0u64;
    let mut knowledge_count = 0u64;

    // Collect IDs to forget before mutating self
    let mut to_forget: Vec<String> = Vec::new();

    for (id, mut engram) in entries {
        // v0.11.0: Both Episode and Knowledge engrams participate.
        // Episode uses default decay scale (1.0), Knowledge uses slower rate.
        let kind_decay_scale = match engram.kind {
            EngramKind::Knowledge => {
                brain.config.vitality.knowledge_decay_rate / brain.config.vitality.episode_decay_rate
            }
            _ => 1.0,
        };

        if engram.kind == EngramKind::Knowledge {
            knowledge_count += 1;
        }

        // 永久保护的不参与衰减
        if engram.protection == Protection::Permanent {
            continue;
        }

        // v0.10.0: Piggyback archive — turn-type engrams inactive >30 days
        if engram.turn_id.is_some() && (now - engram.last_activated) > 30 * 24 * 3600 * 1000 {
            engram.is_archived = true;
            let mut txn = brain.storage.begin_write()?;
            brain.storage.put_hippocampus(&mut txn, &id, &engram)?;
            txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
            report.turns_archived += 1;
            continue;
        }

        let hours_since_active = (now - engram.last_activated).max(0) as f64 / 3_600_000.0;
        if hours_since_active < 0.5 {
            continue; // 很新的记忆跳过本轮
        }

        // 计算干扰: 用 Hopfield 找近邻相似度
        let neighbors = brain.hopfield_prerank(&engram.vector, 10);
        let recent_similar: Vec<f32> = neighbors
            .iter()
            .filter(|(nid, _)| nid.as_str() != id.as_str())
            .map(|(_, sim)| *sim)
            .collect();

        let ctx = vitality::DecayContext {
            hours_since_last_activated: hours_since_active,
            recent_similar,
            lambda: brain.personality.decay_lambda(),
            interference_alpha: brain.personality.interference_alpha(),
            arousal_beta: brain.personality.arousal_beta(),
        };

        let new_vitality = vitality::compute_vitality(
            engram.vitality,
            engram.arousal,
            engram.activation_count,
            engram.last_activated,
            &ctx,
            kind_decay_scale,
        );

        if new_vitality < 0.01 {
            to_forget.push(id.clone());
            forgotten += 1;
        } else if new_vitality < 0.1 {
            engram.is_archived = true;
            engram.vitality = new_vitality;
            let mut txn = brain.storage.begin_write()?;
            brain.storage.put_hippocampus(&mut txn, &id, &engram)?;
            txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
            archived += 1;
        } else {
            engram.vitality = new_vitality;
            let mut txn = brain.storage.begin_write()?;
            brain.storage.put_hippocampus(&mut txn, &id, &engram)?;
            txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
            decayed += 1;
        }
    }

    // 遗忘：从 Hippocampus + Hopfield + Graph 中删除
    for id in &to_forget {
        brain.hopfield.remove_pattern(id);
        let _ = brain.graph.remove_node(&brain.storage, id);
        let mut txn = brain.storage.begin_write()?;
        let _ = brain.storage.delete_hippocampus(&mut txn, id);
        txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
    }
    // 也清理 Hippocampus 内存索引
    if !to_forget.is_empty() {
        let _ = brain.hippocampus.remove_batch(&brain.storage, &to_forget);
    }

    report.vitality_decayed = decayed as usize;
    report.archived_count = archived as usize;
    report.forgotten_count = forgotten as usize;
    report.knowledge_processed = knowledge_count as usize;
    brain.growth.total_forgotten += forgotten;
    Ok(())
}

// ── NREM-2b: Turn Crystallizer ─────────────────────────

/// v0.9.1: Turn Crystallizer — 将相似对话轮次聚类为 Schema。
pub(crate) fn nrem_turn_crystallizer(brain: &mut Brain, report: &mut DreamReport) -> Result<()> {
    let rtxn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let turns = brain.storage.all_dialogues(&rtxn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(rtxn);

    if turns.len() < 3 {
        return Ok(());
    }

    let now = super::now_millis();
    let schemas = crate::schema::turn_cluster_emergence(&turns, 0.85, now);

    for (schema_engram, schema_extra) in schemas {
        brain.hippocampus.store(&brain.storage, &schema_engram)?;
        brain.hopfield.add_pattern(&schema_engram.id, &schema_engram.vector);
        let mut txn = brain.storage.begin_write()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        brain.storage.put_schema(&mut txn, &schema_engram.id, &schema_extra)
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        txn.commit()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;

        // Hebbian-enhanced bidirectional edges: turn → Schema (weight=2.0, Hierarchical)
        for turn_id in &schema_extra.source_episodes {
            if let Err(e) = brain.graph.add_bidirectional_edge(
                &brain.storage,
                turn_id,
                &schema_engram.id,
                2.0,
                AssociationKind::Hierarchical,
                now,
            ) {
                eprintln!(
                    "[dream] Hebbian edge failed for turn {} → schema {}: {e}",
                    turn_id, schema_engram.id
                );
            }
        }

        report.turn_schemas_created += 1;
    }
    Ok(())
}

// ── NREM (v0.12.1): EntanglementEvent decay ────────────

/// v0.12.1: 衰减纠缠事件强度。
/// 超过 30 天未命中的事件每天衰减 10%，强度 < 0.1 时删除。
pub(crate) fn nrem_entanglement_decay(brain: &mut Brain, report: &mut DreamReport) -> Result<()> {
    let now = super::now_millis();
    let rtxn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let events = brain.storage.get_all_entanglements(&rtxn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(rtxn);

    for event in &events {
        let days_since = (now - event.last_hit_at).max(0) / 86_400_000;
        if days_since > 30 {
            let decay = 0.9_f32.powi((days_since - 30) as i32);
            let new_strength = event.strength * decay;

            if new_strength < 0.1 {
                // Delete the event
                let mut wtxn = brain.storage.begin_write()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                for node_id in &event.nodes {
                    let _ = brain.storage.remove_entanglement_node(&mut wtxn, node_id, &event.id);
                }
                brain.storage.delete_entanglement(&mut wtxn, &event.id)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                wtxn.commit()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                report.entanglements_decayed += 1;
            } else {
                // Update strength
                let mut wtxn = brain.storage.begin_write()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                let mut updated = event.clone();
                updated.strength = new_strength;
                brain.storage.put_entanglement(&mut wtxn, &updated)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                wtxn.commit()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
            }
        }
    }

    Ok(())
}

// ── NREM-3: 矛盾检测（增量） ─────────────────────────

/// 扫描 Hippocampus 中的 Episode，用 Hopfield 找近邻（top-20），
/// 对 cosine > 0.8 且关键词重叠低的候选对建立 Contradicts 边。
pub(crate) fn nrem_contradiction_detection(brain: &mut Brain, report: &mut DreamReport) -> Result<()> {
    let entries = brain.hippocampus.all_entries(&brain.storage)?;
    let episodes: Vec<(String, crate::engram::Engram)> = entries
        .into_iter()
        .filter(|(_, e)| e.kind == EngramKind::Episode)
        .collect();

    if episodes.len() < 2 {
        return Ok(());
    }

    let now = super::now_millis();
    let mut detected = 0u32;

    for i in 0..episodes.len() {
        let neighbors = brain.hopfield_prerank(&episodes[i].1.vector, 20);

        for (neighbor_id, sim) in &neighbors {
            if *sim <= 0.8 {
                continue;
            }
            // 找邻居在 episodes 中的索引
            if let Some(j) = episodes.iter().position(|(id, _)| id.as_str() == neighbor_id.as_str()) {
                if j <= i {
                    continue; // 避免重复对
                }
                // 关键词重叠度: 低重叠 + 高 cosine → 矛盾嫌疑
                let overlap = super::keyword_overlap(&episodes[i].1.keywords, &episodes[j].1.keywords);
                if overlap < 0.3 {
                    brain.graph.add_edge(
                        &brain.storage,
                        &episodes[i].0,
                        neighbor_id,
                        *sim,
                        AssociationKind::Contradicts,
                        now,
                    )?;
                    brain.graph.add_edge(
                        &brain.storage,
                        neighbor_id,
                        &episodes[i].0,
                        *sim,
                        AssociationKind::Contradicts,
                        now,
                    )?;
                    detected += 1;
                }
            }
        }
    }

    report.conflicts_detected = detected as usize;
    brain.growth.total_contradictions += detected as u64;
    Ok(())
}
