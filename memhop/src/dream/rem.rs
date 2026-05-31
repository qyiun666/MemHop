//! REM (Rapid Eye Movement) 阶段 — 整合、Schema 涌现、跨 Anchor 发现、三观涌现

use std::collections::HashSet;

use crate::brain::Brain;
use crate::engram::{AssociationKind, Engram, EngramKind};
use crate::error::{MemHopError, Result};
use crate::llm_provider::LlmProvider;
use crate::schema;
use crate::types::DreamReport;
use crate::entanglement::{EntanglementEvent, EntanglementTrigger};
use crate::worldview::{PatternCategory, WorldviewPattern};

// ── REM-1: Hippocampus → Neocortex ──────────────────

/// 将 Hippocampus 中的记忆整合到 Neocortex（Hopfield + Graph）。
/// - cosine > 0.9 → Semantic 边（关联已有节点）
/// - 否则 → 独立插入 Hopfield
/// - 建立 Temporal 边
pub(crate) fn rem_consolidate(brain: &mut Brain, report: &mut DreamReport) -> Result<()> {
    let entries = brain.hippocampus.all_entries(&brain.storage)?;
    if entries.is_empty() {
        return Ok(());
    }

    let now = super::now_millis();
    let mut consolidated = Vec::new();
    let mut edge_count = 0;

    for (id, engram) in &entries {
        let query_f32: Vec<f32> = engram.vector.iter().map(|x| x.to_f32()).collect();
        let neighbors = brain.hopfield.recall_topk(&query_f32, 5);
        let mut merged = false;

        for (neighbor_id, sim) in &neighbors {
            if *sim > 0.9 {
                // 高度相似 → 创建 Semantic 双向边
                brain.graph.add_edge(
                    &brain.storage, id, neighbor_id, *sim, AssociationKind::Semantic, now,
                )?;
                brain.graph.add_edge(
                    &brain.storage, neighbor_id, id, *sim, AssociationKind::Semantic, now,
                )?;
                edge_count += 2;
                merged = true;
                break;
            }
        }

        if !merged {
            // 独立插入 Neocortex
            brain.hopfield.add_pattern(id, &engram.vector);
            // 与同批其他未合并的记忆建立 Temporal 边
            for (other_id, _) in entries.iter().filter(|(oid, _)| *oid != id.as_str()) {
                if !consolidated.contains(other_id) {
                    brain.graph.add_edge(
                        &brain.storage, id, other_id, 0.3, AssociationKind::Temporal, now,
                    )?;
                    edge_count += 1;
                }
            }
        }
        consolidated.push(id.clone());
    }

    // 从 Hippocampus 删除已整合的记忆
    if !consolidated.is_empty() {
        brain.hippocampus.remove_batch(&brain.storage, &consolidated)?;
    }

    report.consolidated_count = consolidated.len();
    report.new_edges = edge_count;
    brain.growth.total_consolidated += consolidated.len() as u64;
    Ok(())
}

// ── REM-2: Schema 涌现 ───────────────────────────────

/// 对 Hippocampus 中的 Episode 和 Knowledge 进行增量聚类。
/// 用 Hopfield 找近邻（top-10），cosine > 0.7 分入同一簇，
/// 簇大小 ≥3 时调用 schema::try_emerge_schema 创建 Schema。
pub(crate) fn rem_schema_emergence(brain: &mut Brain, report: &mut DreamReport) -> Result<()> {
    let entries = brain.hippocampus.all_entries(&brain.storage)?;
    // v0.11.0: Include both Episode and Knowledge engrams for schema emergence
    let episodes: Vec<(String, Engram)> = entries
        .into_iter()
        .filter(|(_, e)| {
            (e.kind == EngramKind::Episode || e.kind == EngramKind::Knowledge) && !e.is_archived
        })
        .collect();

    if episodes.len() < 3 {
        return Ok(());
    }

    let now = super::now_millis();
    let mut new_schemas = 0;
    let mut assigned: HashSet<usize> = HashSet::new();

    for i in 0..episodes.len() {
        if assigned.contains(&i) {
            continue;
        }

        // 用 Hopfield 找当前 Episode 的近邻
        let query: Vec<f32> = episodes[i].1.vector.iter().map(|x| x.to_f32()).collect();
        let neighbors = brain.hopfield.recall_topk(&query, 10);

        // 筛选出相似度 > 0.7 且未分配的 episodes
        let mut cluster: Vec<usize> = vec![i];
        for (nid, sim) in &neighbors {
            if *sim > 0.7
                && let Some(idx) = episodes.iter().position(|(id, _)| id.as_str() == nid.as_str())
                && !assigned.contains(&idx) && idx != i
            {
                cluster.push(idx);
                assigned.insert(idx);
            }
        }
        assigned.insert(i);

        if cluster.len() >= 3 {
            // v0.11.0: Detect cross-kind clusters (Episode + Knowledge)
            let has_episode = cluster.iter().any(|&idx| episodes[idx].1.kind == EngramKind::Episode);
            let has_knowledge = cluster.iter().any(|&idx| episodes[idx].1.kind == EngramKind::Knowledge);
            if has_episode && has_knowledge {
                report.cross_kind_new_associations += 1;
            }

            let cluster_ids: Vec<String> = cluster.iter().map(|&idx| episodes[idx].0.clone()).collect();
            let cluster_engrams: Vec<&Engram> = cluster.iter().map(|&idx| &episodes[idx].1).collect();

            if let Some((schema_engram, schema_extra)) =
                schema::try_emerge_schema(&cluster_ids, &cluster_engrams, now)
            {
                // 存 Schema 到 Hippocampus
                brain.hippocampus.store(&brain.storage, &schema_engram)?;
                // 注册到 Hopfield
                brain.hopfield.add_pattern(&schema_engram.id, &schema_engram.vector);
                // 持久化 SchemaExtra
                let mut txn = brain.storage.begin_write()?;
                brain.storage.put_schema(&mut txn, &schema_engram.id, &schema_extra)?;
                txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
                new_schemas += 1;
            }
        }
    }

    report.new_schemas = new_schemas;
    brain.growth.total_schemas_emerged += new_schemas as u64;
    Ok(())
}

// ── REM-3: 跨 Anchor 发现（增量） ────────────────────

/// 扫描已有 Anchor，跨 Anchor 发现 cosine > 0.8 的记忆对并建立 Semantic 边。
pub(crate) fn rem_cross_anchor_discovery(brain: &mut Brain, report: &mut DreamReport) -> Result<()> {
    let txn = brain.storage.begin_read()?;
    let anchor_names = brain.storage.all_anchor_names(&txn)?;
    drop(txn);

    if anchor_names.len() < 2 {
        return Ok(());
    }

    let now = super::now_millis();
    let mut new_edges = 0u32;

    for i in 0..anchor_names.len() {
        let txn = brain.storage.begin_read()?;
        let ids_a = brain.storage.anchor_get_ids(&txn, &anchor_names[i])?;
        drop(txn);

        for other_name in anchor_names.iter().skip(i + 1) {
            let txn = brain.storage.begin_read()?;
            let ids_b = brain.storage.anchor_get_ids(&txn, other_name)?;
            drop(txn);

            // 取每个 Anchor 下前 3 条记忆做跨 Anchor 比较
            for id_a in ids_a.iter().take(3) {
                let txn = brain.storage.begin_read()?;
                let engram_a = brain.storage.get_hippocampus(&txn, id_a)?;
                drop(txn);

                if let Some(engram_a) = engram_a {
                    let query_f32: Vec<f32> =
                        engram_a.vector.iter().map(|x| x.to_f32()).collect();
                    let neighbors = brain.hopfield.recall_topk(&query_f32, 10);

                    for (neighbor_id, sim) in &neighbors {
                        if *sim > 0.8 && ids_b.contains(neighbor_id) {
                            brain.graph.add_edge(
                                &brain.storage,
                                id_a,
                                neighbor_id,
                                *sim,
                                AssociationKind::Semantic,
                                now,
                            )?;
                            brain.graph.add_edge(
                                &brain.storage,
                                neighbor_id,
                                id_a,
                                *sim,
                                AssociationKind::Semantic,
                                now,
                            )?;
                            new_edges += 2;
                        }
                    }
                }
            }
        }
    }

    report.new_edges += new_edges as usize;
    Ok(())
}

// ── REM (v0.12.1): EntanglementEvent 创建 ────────────

/// v0.12.1: Dream REM 阶段 — 检测跨 Anchor 的跨树纠缠，创建 EntanglementEvent。
pub(crate) fn rem_entanglement_creation(brain: &mut Brain, report: &mut DreamReport) -> Result<()> {
    let txn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let anchor_names = brain.storage.all_anchor_names(&txn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(txn);

    if anchor_names.len() < 2 {
        return Ok(());
    }

    for i in 0..anchor_names.len() {
        let txn = brain.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let ids_a = brain.storage.anchor_get_ids(&txn, &anchor_names[i])
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        drop(txn);

        for other_name in anchor_names.iter().skip(i + 1) {
            let txn = brain.storage.begin_read()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            let ids_b = brain.storage.anchor_get_ids(&txn, other_name)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            drop(txn);

            // Collect engrams with tree_refs from both anchors
            let mut tree_ids_set: HashSet<String> = HashSet::new();
            let mut node_ids: Vec<String> = Vec::new();

            for id in ids_a.iter().chain(ids_b.iter()) {
                let txn = brain.storage.begin_read()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                if let Ok(Some(engram)) = brain.storage.get_hippocampus(&txn, id)
                    && let Some(ref tr) = engram.tree_ref
                {
                    tree_ids_set.insert(tr.tree_id.clone());
                    if !node_ids.contains(&engram.id) {
                        node_ids.push(engram.id.clone());
                    }
                }
                drop(txn);
            }

            if tree_ids_set.len() >= 2 && node_ids.len() >= 2 {
                let context = format!(
                    "Dream REM 跨 Anchor 纠缠: {} <-> {}",
                    anchor_names[i], other_name,
                );
                let tree_ids: Vec<String> = tree_ids_set.into_iter().collect();
                crate::entanglement::create_or_update_entanglement(
                    brain,
                    node_ids,
                    tree_ids,
                    context,
                    EntanglementTrigger::DreamEmergence,
                );
                report.entanglements_created += 1;
            }
        }
    }

    Ok(())
}

// ── REM (v0.12.1): 三观涌现 ─────────────────────────

/// v0.12.1: REM 阶段 — 从纠缠事件涌现三观模式。
///
/// 对 strength > 0.5 的纠缠事件按 context 关键词聚类，
/// 每类 ≥3 事件且平均稳定度 ≥0.3 则创建或更新 WorldviewPattern。
pub(crate) fn rem_worldview_emergence(brain: &mut Brain, report: &mut DreamReport) -> Result<()> {
    let rtxn = brain.storage.begin_read()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    // 1. 获取所有纠缠事件（strength > 0.5）
    let events = brain.storage.get_all_entanglements(&rtxn)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    drop(rtxn);

    if events.len() < 10 {
        return Ok(());
    }

    let strong_events: Vec<&EntanglementEvent> = events.iter()
        .filter(|e| e.strength > 0.5)
        .collect();

    if strong_events.len() < 5 {
        return Ok(());
    }

    // 2. 对 event.context 做简单语义聚类（按关键词重叠分组）
    let mut clusters: Vec<Vec<(&EntanglementEvent, Vec<String>)>> = Vec::new();

    for event in &strong_events {
        let keywords: Vec<String> = event.context
            .split(|c: char| !c.is_alphanumeric() && c != '_')
            .filter(|s| s.len() > 1)
            .map(|s| s.to_lowercase())
            .collect();

        // 找最匹配的已有聚类
        let mut best_cluster = None;
        for (ci, cluster) in clusters.iter().enumerate() {
            let cluster_keywords: Vec<&str> = cluster.iter()
                .flat_map(|(_, kw)| kw.iter().map(|s| s.as_str()))
                .collect();
            let overlap = keywords.iter()
                .filter(|k| cluster_keywords.contains(&k.as_str()))
                .count();
            if overlap >= 2 {
                best_cluster = Some(ci);
                break;
            }
        }

        if let Some(ci) = best_cluster {
            clusters[ci].push((event, keywords));
        } else {
            clusters.push(vec![(event, keywords)]);
        }
    }

    // 3. 为每个类簇创建或更新 WorldviewPattern
    let now = super::now_millis();
    let mut emerged = 0usize;

    for cluster in &clusters {
        if cluster.len() < 3 {
            continue;
        }

        let avg_strength: f32 = cluster.iter().map(|(e, _)| e.strength).sum::<f32>() / cluster.len() as f32;
        let occurrence = cluster.len() as u64;
        let stability = (1.0_f32.min(occurrence as f32 / 10.0)) * avg_strength;

        if stability < 0.3 {
            continue;
        }

        // 生成模式描述
        let source_ids: Vec<String> = cluster.iter().map(|(e, _)| e.id.clone()).collect();
        let contexts: Vec<&str> = cluster.iter().map(|(e, _)| e.context.as_str()).collect();
        let pattern_text = contexts.join("; ");

        // 分类（简化版）
        let category = PatternCategory::ThinkingStyle;

        // 检查是否已有类似的世界观（通过 source_events 重叠）
        let rtxn = brain.storage.begin_read()
            .map_err(|e| MemHopError::Storage(e.to_string()))?;
        let existing = brain.storage.get_all_worldviews(&rtxn)
            .unwrap_or_default();
        drop(rtxn);

        let mut updated = false;
        for old_wv in &existing {
            let old_events: Vec<&str> = old_wv.source_events.iter().map(|s| s.as_str()).collect();
            let new_events: Vec<&str> = source_ids.iter().map(|s| s.as_str()).collect();
            let overlap = old_events.iter().filter(|e| new_events.contains(e)).count();
            if overlap >= 2 {
                // 更新已有模式
                let mut wtxn = brain.storage.begin_write()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                let mut updated_wv = old_wv.clone();
                updated_wv.occurrence_count += occurrence;
                updated_wv.stability = (updated_wv.stability + stability) / 2.0;
                updated_wv.last_reinforced_at = now;
                // 合并 source_events（去重）
                for sid in &source_ids {
                    if !updated_wv.source_events.contains(sid) {
                        updated_wv.source_events.push(sid.clone());
                    }
                }
                brain.storage.put_worldview(&mut wtxn, &updated_wv)
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                wtxn.commit()
                    .map_err(|e| MemHopError::Storage(e.to_string()))?;
                updated = true;
                break;
            }
        }

        if !updated {
            // 创建新 WorldviewPattern
            let id = crate::brain::generate_id();
            let wv = WorldviewPattern {
                id: id.clone(),
                source_events: source_ids,
                pattern: pattern_text.chars().take(200).collect(),
                category,
                occurrence_count: occurrence,
                stability,
                emerged_at: now,
                last_reinforced_at: now,
            };
            let mut wtxn = brain.storage.begin_write()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            brain.storage.put_worldview(&mut wtxn, &wv)
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            wtxn.commit()
                .map_err(|e| MemHopError::Storage(e.to_string()))?;
            emerged += 1;
        }
    }

    report.worldviews_emerged = emerged;
    Ok(())
}

// ── LLM-enhanced dream phases ─────────────────────────

/// If an LlmProvider is configured, suggest keywords for every engram in
/// Hippocampus whose keyword list is empty. Updated engrams are written
/// back to storage in-place.
pub(crate) fn dream_llm_keywords(brain: &Brain, llm: &dyn LlmProvider, report: &mut DreamReport) -> Result<()> {
    let entries = brain.hippocampus.all_entries(&brain.storage)?;
    let mut count = 0usize;

    for (id, mut engram) in entries {
        if !engram.keywords.is_empty() {
            continue;
        }
        match crate::llm_provider::llm_suggest_keywords(llm, &engram.text) {
            Ok(kws) if !kws.is_empty() => {
                engram.keywords = kws;
                let mut txn = brain.storage.begin_write()?;
                brain.storage.put_hippocampus(&mut txn, &id, &engram)?;
                txn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;
                count += 1;
            }
            Ok(_) => { /* LLM returned empty list -- skip */ }
            Err(e) => eprintln!("[dream] LLM suggest_keywords for {}: {}", id, e),
        }
    }

    report.llm_keywords_added = count;
    Ok(())
}

/// If an LlmProvider is configured, verify high-cosine, low-keyword-overlap
/// pairs with the LLM before marking them as contradictions. This runs in
/// addition to (not instead of) the heuristic check in nrem_contradiction_detection.
pub(crate) fn dream_llm_contradictions(brain: &mut Brain, llm: &dyn LlmProvider, report: &mut DreamReport) -> Result<()> {
    let entries = brain.hippocampus.all_entries(&brain.storage)?;
    let episodes: Vec<(String, Engram)> = entries
        .into_iter()
        .filter(|(_, e)| e.kind == EngramKind::Episode)
        .collect();

    if episodes.len() < 2 {
        return Ok(());
    }

    let now = super::now_millis();
    let mut detected = 0usize;

    for i in 0..episodes.len() {
        let query_f32: Vec<f32> = episodes[i].1.vector.iter().map(|x| x.to_f32()).collect();
        let neighbors = brain.hopfield.recall_topk(&query_f32, 20);

        for (neighbor_id, sim) in &neighbors {
            if *sim <= 0.8 {
                continue;
            }
            if let Some(j) = episodes.iter().position(|(id, _)| id.as_str() == neighbor_id.as_str()) {
                if j <= i {
                    continue;
                }
                let overlap = super::keyword_overlap(&episodes[i].1.keywords, &episodes[j].1.keywords);
                if overlap < 0.3 {
                    match crate::llm_provider::llm_detect_contradiction(
                        llm,
                        &episodes[i].1.text,
                        &episodes[j].1.text,
                    ) {
                        Ok(true) => {
                            brain.graph.add_edge(
                                &brain.storage, &episodes[i].0, neighbor_id,
                                *sim, AssociationKind::Contradicts, now,
                            )?;
                            brain.graph.add_edge(
                                &brain.storage, neighbor_id, &episodes[i].0,
                                *sim, AssociationKind::Contradicts, now,
                            )?;
                            detected += 1;
                        }
                        Ok(false) => { /* LLM says not contradictory -- skip */ }
                        Err(e) => eprintln!("[dream] LLM contradiction check: {}", e),
                    }
                }
            }
        }
    }

    report.llm_contradictions = detected;
    brain.growth.total_contradictions += detected as u64;
    Ok(())
}
