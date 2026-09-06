// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Built-in capability assembly for the composition root. The manuals an LLM
// reads before it calls anything are assembled here in code — the public api
// methods rendered as memhop-capability/v4-shaped cards with stable
// name-derived ids, status active and Origin builtin. They are served by the
// L5 read APIs but never stored: a write to a built-in-only id reports
// ErrNotFound like any record that is not there, and a stored record with the
// same id (e.g. from a plug/ package) shadows its built-in twin in listings.
// Host-provided manuals live in <meh dir>/plug/<package>/capability.json and
// are injected on every Open (l5plug.go).

package internal

import (
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// BuiltinCards assembles the engine's read-only reference cards — one per
// manual, covering every api.Session business method. Descriptions that
// enumerate engine value sets (edge kinds, content types) are pinned by the
// api package's vocabulary tests, which walk the enums against them.
func BuiltinCards() []core.Capability {
	cards := cards()
	for i := range cards {
		c := &cards[i]
		c.IDHash = core.CapabilityID(c.Name)
		c.Package = c.Name
		c.Status = core.CapabilityActive
		c.Origin = core.CapabilityOriginBuiltin
	}
	return cards
}

func cards() []core.Capability {
	return []core.Capability{
		{
			Name: "memhop-guide", Version: "4",
			Summary: "MemHop memory-loop overview: Search (read one scene and open the turn), Update (settle the finished turn into the topic Search minted), Dream (consolidation) and trajectory recording all run automatically on the host — the LLM must NOT call them manually (trajectories older than 7 days are pruned automatically); the abilities an LLM may call are indexed in resources; to use a card, resolve its 16-char hex capability id from the host card index (ListCapabilities projection carries id alongside name), then read that one card in full by listing with an id filter (Go ListCapabilities with IDs set to that single id; MCP tool memhop_capability_get) to get its parameter schemas",
			Trigger: "using the memhop memory system, unsure what is callable or which card fits the task; guide overview memory loop entry index",
			Resources: []core.ResourceRef{
				{Type: core.CapabilityAPI, Name: "memhop-cycle", Ref: "capability:memhop-cycle",
					Desc: "Core memory loop (Search/Update/Dream): what each turn does — host-driven, for background understanding"},
				{Type: core.CapabilityAPI, Name: "memhop-scene", Ref: "capability:memhop-scene",
					Desc: "L2 scene management: list scenes, read topic context, merge scenes, delete wrong memories (memory correction)"},
				{Type: core.CapabilityAPI, Name: "memhop-knowledge", Ref: "capability:memhop-knowledge",
					Desc: "L3 knowledge graph: import/query entities and relations, expand subgraphs (use for durable facts, people, projects)"},
				{Type: core.CapabilityAPI, Name: "memhop-archive", Ref: "capability:memhop-archive",
					Desc: "L4 archive: search historical conversation text by keyword / time range / IDs (look up past raw messages)"},
				{Type: core.CapabilityAPI, Name: "memhop-profile", Ref: "capability:memhop-profile",
					Desc: "L0 profile: read/update the host identity profile (use when the user actively corrects their profile)"},
				{Type: core.CapabilityAPI, Name: "memhop-capability", Ref: "capability:memhop-capability",
					Desc: "L5 capability loop: crystallize reusable abilities from trajectories, import external capabilities, status patches, usage feedback, list/get-detail"},
				{Type: core.CapabilityAPI, Name: "memhop-trajectory", Ref: "capability:memhop-trajectory",
					Desc: "L6 trajectory: append/read the per-turn event log, list trajectory keys, crystallize drafts"},
				{Type: core.CapabilityAPI, Name: "memhop-plan", Ref: "capability:memhop-plan",
					Desc: "L6 plan tree: sync the host's task tree, commit step statuses, read the forest view (Go host only)"},
			},
		},
		{
			Name: "memhop-cycle", Version: "4",
			Summary: "Core memory-loop manual: one scene = one host session, one turn = one topic. Search reads the scene and opens the turn (zero LLM), Update settles the finished turn into the topic id Search issued (one distillation), Dream consolidates. On a wired host these run automatically around each turn — an LLM driving raw MCP tools should not call them out of band",
			Trigger: "unsure what Search Update Dream do per turn, when memory is written or consolidated; cycle loop search update dream turn scene consolidation",
			Resources: []core.ResourceRef{
				{Type: core.CapabilityAPI, Name: "Search", Ref: "api:Search",
					Desc:   "Read one scene (the host's session) and open the turn about to run: returns the L0 profile snapshot plus compact brief, the scene's depth-1 topics (each with fused keywords and archive refs) and new_topic_id — the id this read minted for the coming turn. An empty scene_id allocates a fresh scene; an unknown scene_id is ErrNotFound. No LLM is contacted",
					Input:  `{"type":"object","properties":{"scene_id":{"type":"string","description":"Scene id, 16-char hex; omit to open a new scene"},"l3_id":{"type":"string","description":"On a fresh scene only: anchor it to this L3 project domain (16-char hex)"}}}`,
					Output: "SearchResult (Profile/ProfileBrief/Scene/Topics/NewTopicID)"},
				{Type: core.CapabilityAPI, Name: "Update", Ref: "api:Update",
					Desc:   "Settle one finished turn into the topic id Search issued: both raw texts (user + agent) with unix-ms timestamps, content types optional (text|image|video|document|audio|code|other). One LLM distillation runs before any write — a failure leaves nothing behind. Re-sending the same topic_id overwrites that turn, so retries are safe",
					Input:  `{"type":"object","properties":{"scene_id":{"type":"string","description":"Existing scene id (16-char hex)"},"topic_id":{"type":"string","description":"The new_topic_id Search returned for this turn (16-char hex)"},"user_text":{"type":"string"},"user_ts":{"type":"integer","description":"unix ms > 0"},"agent_text":{"type":"string"},"agent_ts":{"type":"integer"}},"required":["scene_id","topic_id","user_text","user_ts","agent_text","agent_ts"]}`,
					Output: "the settled topic id (16-char hex)"},
				{Type: core.CapabilityAPI, Name: "Dream", Ref: "api:Dream",
					Desc:   "Consolidation pass (the sleep analogue): fuses/compresses L2 topics, rebuilds and decays the L1 graph, distills the L0 profile, prunes L6 events past the 7-day retention window. Usually scheduled automatically when a scene grows past the topic threshold; an explicit call at idle is welcome. Contacts the LLM repeatedly inside the domain lock — slow",
					Input:  `{"type":"object","properties":{"scene_id":{"type":"string","description":"One scene (16-char hex); omit for every scene of the domain"}}}`,
					Output: "DreamReport (consolidated scenes, compressed topics, L1 nodes/edges, L0 updated, per-stage statuses)"},
			},
		},
		{
			Name: "memhop-profile", Version: "4",
			Summary: "L0 profile manual: read/update the host identity profile (role, personality, emotion state, MBTI, preferences). The entries below are Go api methods; an MCP client calls the same operations as memhop_profile_get / memhop_profile_update. Typical order: GetL0 -> UpdateL0.",
			Trigger: "need to view or adjust the host profile, identity, preferences; profile l0 identity role personality emotion mbti preference",
			Resources: []core.ResourceRef{
				{Type: core.CapabilityAPI, Name: "GetL0", Ref: "api:GetL0",
					Desc:   "Read the current L0 profile; returns Name/Role/Personality/EmotionState/MBTI/Preferences",
					Input:  `{"type":"object","properties":{}}`,
					Output: "ProfileSlot (Name/Role/Personality/EmotionState{Valence,Arousal,Dominance}/MBTI{IE,NS,TF,JP,Type}/Preferences/UpdatedAtMs)"},
				{Type: core.CapabilityAPI, Name: "UpdateL0", Ref: "api:UpdateL0",
					Desc:   "Write the host-owned half of the profile: name, role, personality and preferences (a key-value map). EmotionState and MBTI belong to Dream's distillation and are always kept from the stored profile, and updated_at_ms is stamped by the library — neither is accepted on input, so there is no need to GetL0 and fill values back first",
					Input:  `{"type":"object","properties":{"name":{"type":"string"},"role":{"type":"string"},"personality":{"type":"string"},"preferences":{"type":"object","additionalProperties":{"type":"string"}}}}`,
					Output: "ok"},
			},
		},
		{
			Name: "memhop-scene", Version: "4",
			Summary: "L2 scene manual: a scene is one host session — list scenes, read topic context within a scene, rename a scene, merge sessions into one, delete topics and scenes (memory correction). The entries below are Go api methods; an MCP client calls the same operations as memhop_scene_list / _topics / _rename / _merge, while DeleteTopic and DeleteScene stay Go-only (no MCP tool). Typical order: ListScenes -> SceneContext -> UpdateScene -> MergeScenes -> DeleteTopic -> DeleteScene.",
			Trigger: "need to view scene lists, read a scene's topic context, name a session readably, merge duplicate scenes or delete wrong memories; scene l2 topic rename merge delete context",
			Resources: []core.ResourceRef{
				{Type: core.CapabilityAPI, Name: "ListScenes", Ref: "api:ListScenes",
					Desc:   "List L2 scenes; an optional l3_id keeps only the scenes anchored to that project domain. Returns SceneID/name/topic count",
					Input:  `{"type": "object", "properties": {"l3_id": {"type": "string", "description": "Only scenes anchored to this L3 domain, 16-char hex (optional)"}}}`,
					Output: "List of SceneSlot"},
				{Type: core.CapabilityAPI, Name: "SceneContext", Ref: "api:SceneContext",
					Desc:   "Read scene context; returns SceneName/Topics (depth-1 topic metadata incl. L4IDs to fetch raw text by ID) and TopicCount",
					Input:  `{"type":"object","properties":{"scene_id":{"type":"string","description":"Scene ID (16-char hex)"}},"required":["scene_id"]}`,
					Output: "SceneContext (SceneName/Topics/TopicCount)"},
				{Type: core.CapabilityAPI, Name: "UpdateScene", Ref: "api:UpdateScene",
					Desc:   "Patch a scene's host-facing metadata in one write and get the stored scene back. Name renames it (a fresh scene is library-named \"session:<id>\"); L3ID anchors it to an L3 project domain, and an empty L3ID clears the anchor. Omitted fields keep their stored value. Anchoring is write-once: moving a scene that already has a different domain needs Force. The target domain must exist",
					Input:  `{"type": "object", "properties": {"scene_id": {"type": "string", "description": "Scene id, 16-char hex (required)"}, "name": {"type": "string", "description": "New title; omit to keep the current one"}, "l3_id": {"type": "string", "description": "L3 project domain to anchor (16-char hex); \"\" clears it; omit to keep"}, "force": {"type": "boolean", "description": "Allow replacing a different existing anchor"}}, "required": ["scene_id"]}`,
					Output: "ok"},
				{Type: core.CapabilityAPI, Name: "MergeScenes", Ref: "api:MergeScenes",
					Desc:   "Merge scenes: primaryID=target scene, secondaryIDs=list of scenes to fold in; all topics of secondary scenes move into the primary scene, then the secondaries are deleted",
					Input:  `{"type":"object","properties":{"primary_id":{"type":"string","description":"Primary scene ID (16-char hex)"},"secondary_ids":{"type":"array","items":{"type":"string"},"description":"Secondary scene IDs (16-char hex)"}},"required":["primary_id","secondary_ids"]}`,
					Output: "ok"},
				{Type: core.CapabilityAPI, Name: "DeleteTopic", Ref: "api:DeleteTopic",
					Desc:   "Delete one L2 topic with its subtree and L4 raw text (memory correction); cascades to child topics and prunes parent references. Host Go API only — no MCP tool",
					Input:  `{"type":"object","properties":{"topic_id":{"type":"string","description":"Topic ID (16-char hex)"}},"required":["topic_id"]}`,
					Output: "ok"},
				{Type: core.CapabilityAPI, Name: "DeleteScene", Ref: "api:DeleteScene",
					Desc:   "Delete an entire L2 scene (all topics, L4 raw text and L1 nodes); unknown scene returns ErrNotFound. Host Go API only — no MCP tool",
					Input:  `{"type":"object","properties":{"scene_id":{"type":"string","description":"Scene ID (16-char hex)"}},"required":["scene_id"]}`,
					Output: "ok"},
			},
		},
		{
			Name: "memhop-knowledge", Version: "4",
			Summary: "L3 knowledge-graph manual: read/list/import/update/delete semantic graphs, query nodes and expand subgraphs. The graph pool is file-wide: every agent domain of the file shares one L3 pool (import once, visible to all; deleting an agent never deletes the pool). The entries below are Go api methods; an MCP client calls the same operations as memhop_knowledge_get / _list / _import / _update / _delete / _nodes / _subgraph. Typical order: ListL3 -> ImportL3 -> QueryL3Subgraph -> GetL3 -> UpdateL3 -> DeleteL3.",
			Trigger: "need to manage or query the L3 semantic knowledge graph, structured knowledge; knowledge l3 graph semantic node edge hypergraph",
			Resources: []core.ResourceRef{
				{Type: core.CapabilityAPI, Name: "GetL3", Ref: "api:GetL3",
					Desc:   "Read one L3 knowledge graph by ID",
					Input:  `{"type":"object","properties":{"id":{"type":"string","description":"Graph ID (16-char hex)"}},"required":["id"]}`,
					Output: "L3Graph"},
				{Type: core.CapabilityAPI, Name: "ListL3", Ref: "api:ListL3",
					Desc:   "List all L3 knowledge graphs: graph containers only (id / name / source / timestamps) — no node or edge counts. Read one graph's size with GetL3, or list its nodes with QueryL3Nodes naming only the graph",
					Input:  `{"type":"object","properties":{}}`,
					Output: "List of HypergraphSlot (containers)"},
				{Type: core.CapabilityAPI, Name: "ImportL3", Ref: "api:ImportL3",
					Desc:   "Bulk-import L3 knowledge entries: title/domain/node_type/content per node, keywords optional, source_ref optional positional reference (file:line / URL), related optional same-graph relation edges resolved by title (targets may appear later in the batch; kind is related|causal|part_of|sequence|dependency|custom, default related; an edge is identified by its member set plus kind, so the same nodes can carry several relations, and re-importing the same batch never duplicates an edge) — each relation names its far side in `titles` (one entry = a binary relation, several = one hyperedge spanning the item plus every target, so an n-node fact stays one fact); a relation with no target, an empty, duplicated or self-referencing title, or a target that is not in the graph, is reported in errors and creates nothing; every item must carry title and domain and mode is required (case-sensitive Skip|Merge|Overwrite) — a batch violating any of this is rejected and writes nothing; returns graph_ids (the graphs the batch wrote into — feed these to UpdateScene/Search to anchor a scene) plus created_ids/updated_ids (node ids), edges_created and skipped stats",
					Input:  `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"title":{"type":"string"},"domain":{"type":"string"},"node_type":{"type":"string"},"content":{"type":"string"},"keywords":{"type":"array","items":{"type":"string"}},"source_ref":{"type":"string","description":"Positional reference (file:line / URL)"},"related":{"type":"array","items":{"type":"object","properties":{"title":{"type":"string","description":"Target node title (same graph, may appear later in the batch)"},"kind":{"type":"string","description":"related|causal|part_of|sequence|dependency|custom (default related)"}},"required":["title"]}}},"required":["title","domain","node_type","content"]},"description":"Knowledge entries (nodes) to import"},"mode":{"type":"string","description":"Skip|Merge|Overwrite (case-sensitive)"}},"required":["items","mode"]}`,
					Output: "L3ImportResult (graph_ids + created/updated node ids + edges_created/skipped stats)"},
				{Type: core.CapabilityAPI, Name: "UpdateL3", Ref: "api:UpdateL3",
					Desc:   "Rename an L3 graph; name omitted means no rename. The new label must not already be carried by another graph — a domain label is how ImportL3 addresses a graph, so a collision is refused with ErrInvalidQuery rather than making that domain resolve ambiguously",
					Input:  `{"type":"object","properties":{"id":{"type":"string","description":"Graph ID (16-char hex)"},"name":{"type":"string","description":"New name (omit to keep)"}},"required":["id"]}`,
					Output: "L3Graph (updated)"},
				{Type: core.CapabilityAPI, Name: "DeleteL3", Ref: "api:DeleteL3",
					Desc:   "Delete an entire L3 knowledge graph — the slot plus every node and hyperedge in it, and the L2 scene anchors that named it across every agent domain of the file (graphs live in the file-wide shared pool, so a deleted graph leaves no scene listing under it anywhere). To correct one node without losing the edges bound to the rest, use DeleteL3Nodes (Go-only, no MCP tool)",
					Input:  `{"type":"object","properties":{"id":{"type":"string","description":"Graph ID (16-char hex)"}},"required":["id"]}`,
					Output: "ok"},
				{Type: core.CapabilityAPI, Name: "DeleteL3Nodes", Ref: "api:DeleteL3Nodes",
					Desc:   "Delete specific nodes from one L3 graph, cascading every hyperedge that touches them. Go-only (no MCP tool), like the other memory-correction calls. Every id must name a node of that graph; an unknown or foreign id is refused and nothing is deleted",
					Input:  `{"type":"object","properties":{"graph_id":{"type":"string","description":"Graph ID (16-char hex)"},"node_ids":{"type":"array","items":{"type":"string"},"description":"Node IDs (16-char hex)"}},"required":["graph_id","node_ids"]}`,
					Output: "ok"},
				{Type: core.CapabilityAPI, Name: "QueryL3Nodes", Ref: "api:QueryL3Nodes",
					Desc:   "Query L3 nodes of one graph; graph_id required and every other condition that is set filters, so ids/keyword/node_type AND together (naming only the graph lists its nodes). Keyword is case-insensitive over title, content and keywords; limit caps the result count (<=0 means unlimited)",
					Input:  `{"type":"object","properties":{"graph_id":{"type":"string","description":"Graph ID (16-char hex)"},"ids":{"type":"array","items":{"type":"string"},"description":"Node ID list (16-char hex)"},"keyword":{"type":"string","description":"Keyword match"},"node_type":{"type":"string","description":"Node type filter"},"limit":{"type":"integer","description":"Max results (<=0 unlimited)"}},"required":["graph_id"]}`,
					Output: "List of HypergraphNode"},
				{Type: core.CapabilityAPI, Name: "QueryL3Subgraph", Ref: "api:QueryL3Subgraph",
					Desc:   "Expand a subgraph from a start node; edge_kinds limits relation types (related/causal/part_of/sequence/dependency/custom), max_depth limits expansion depth",
					Input:  `{"type":"object","properties":{"graph_id":{"type":"string","description":"Graph ID (16-char hex)"},"start_node_id":{"type":"string","description":"Start node ID (16-char hex)"},"max_depth":{"type":"integer","description":"Max expansion depth"},"edge_kinds":{"type":"array","items":{"type":"string"},"description":"Edge kind filter"}},"required":["graph_id","start_node_id","max_depth"]}`,
					Output: "L3Subgraph (nodes and edges)"},
			},
		},
		{
			Name: "memhop-archive", Version: "4",
			Summary: "L4 archive manual: search historical conversation text by any combination of keyword / time range / ID / topic / content type. The entries below are Go api methods; an MCP client calls the same operation as memhop_archive_search (memhop_archive_get is the by-id convenience)",
			Trigger: "need to look up past conversation text or dig out old messages; archive l4 raw text history conversation search",
			Resources: []core.ResourceRef{
				{Type: core.CapabilityAPI, Name: "SearchL4", Ref: "api:SearchL4",
					Desc:   "Search L4 raw text. Every condition is optional and the set ones AND together: Keyword (case-insensitive substring, like the L3 node keyword), Start/End (millisecond range), IDs (16-char hex archive ids), TopicID (only that turn's originals), Type (text|image|video|document|audio|code|other). An empty query returns the domain's whole archive set, sorted by CreatedAt oldest first; Limit keeps the newest N matches (<=0 means every match). For media types Content holds a path or URI, not binary",
					Input:  `{"type": "object", "properties": {"keyword": {"type": "string", "description": "Substring match on Content (optional)"}, "start": {"type": "integer", "description": "Created at or after, unix ms (optional)"}, "end": {"type": "integer", "description": "Created at or before, unix ms (optional)"}, "ids": {"type": "array", "items": {"type": "string"}, "description": "Archive ids, 16-char hex (optional)"}, "topic_id": {"type": "string", "description": "Only this topic's archives, 16-char hex (optional)"}, "type": {"type": "string", "description": "Content type filter: text|image|video|document|audio|code|other (optional)"}}}`,
					Output: "List of ArchiveSlot (Content/Role/CreatedAt/Metadata)"},
			},
		},
		{
			Name: "memhop-capability", Version: "4",
			Summary: "L5 capability loop manual: crystallize reusable abilities from L6 trajectories (draft) -> promote via a status patch -> usage feedback, import external capability packages, List/Get index and details. The engine's built-in manuals (this card set) are not stored records: they appear in listings, and a write to one reports not-found. The entries below are Go api methods; an MCP client calls the same operations as memhop_capability_list / _get / _import / _update / _delete / _usage and memhop_crystallize",
			Trigger: "need to distill reusable abilities, import capability definitions, promote drafts, record usage results, view the capability list or fetch card details; capability l5 crystallize import activate draft lifecycle",
			Resources: []core.ResourceRef{
				{Type: core.CapabilityAPI, Name: "RecordCapabilityUsage", Ref: "api:RecordCapabilityUsage",
					Desc:   "Host feedback after a capability was used; updates success rate and trigger count",
					Input:  `{"type":"object","properties":{"id":{"type":"string","description":"Capability ID (16-char hex)"},"success":{"type":"boolean","description":"Whether the call succeeded"}},"required":["id","success"]}`,
					Output: "Capability (SuccessRate/TriggerCount updated)"},
				{Type: core.CapabilityAPI, Name: "ImportCapability", Ref: "api:ImportCapability",
					Desc:   "Import a memhop-capability/v4 package file (single file, or a directory containing capability.json — one package holds 1..N cards); cards land active in the file-wide shared pool (every agent sees them); same-name imports upsert (definition updated, stats preserved), byte-identical content is skipped; the built-in manuals need no import, ListCapabilities returns them directly",
					Input:  `{"type":"object","properties":{"path":{"type":"string","description":"Capability file or directory path"}},"required":["path"]}`,
					Output: "CapabilityImportResult (CreatedIDs/UpdatedIDs/Errors)"},
				{Type: core.CapabilityAPI, Name: "ListCapabilities", Ref: "api:ListCapabilities",
					Desc:   "List/filter capability cards. Every condition is optional and the set ones AND together: IDs (16-char hex — pass one id to read a single card), Status, Package (source package name), Keyword (matches name+summary+trigger). The engine's built-in manuals are included",
					Input:  `{"type": "object", "properties": {"ids": {"type": "array", "items": {"type": "string"}, "description": "Capability ids, 16-char hex (optional)"}, "status": {"type": "string", "description": "draft|active|deprecated (optional)"}, "package": {"type": "string", "description": "Source package name (optional)"}, "keyword": {"type": "string", "description": "Substring of name+summary+trigger (optional)"}}}`,
					Output: "List of Capability (descending by UpdatedAt)"},
				{Type: core.CapabilityAPI, Name: "UpdateCapability", Ref: "api:UpdateCapability",
					Desc:   "Partially update a capability; nil patch fields are left unchanged (Name and Package are immutable). status is the lifecycle switch (draft|active|deprecated): pass active to promote a crystallized draft before use",
					Input:  `{"type":"object","properties":{"id":{"type":"string","description":"Capability ID (16-char hex)"},"version":{"type":"string"},"summary":{"type":"string"},"trigger":{"type":"string"},"status":{"type":"string"},"resources":{"type":"array","items":{"type":"object"}}},"required":["id"]}`,
					Output: "Capability (updated)"},
				{Type: core.CapabilityAPI, Name: "DeleteCapability", Ref: "api:DeleteCapability",
					Desc:   "Delete a capability record; deleting a card that is not there is reported (the built-in manuals are not stored records, so their ids report not-found too)",
					Input:  `{"type":"object","properties":{"id":{"type":"string","description":"Capability ID (16-char hex)"}},"required":["id"]}`,
					Output: "ok"},
			},
		},
		{
			Name: "memhop-trajectory", Version: "4",
			Summary: "L6 trajectory manual: the per-turn event log. A turn's key is the topic id Search minted; a plan's key is its plan id with events bound to dotted node paths. The log is append-only and read whole per key; events older than 7 days are pruned by Dream; there is no delete API. The entries below are Go api methods; an MCP client calls the same operations as memhop_trajectory_append / _read / _sessions and memhop_crystallize",
			Trigger: "need to record or replay what happened inside a turn or plan, feed crystallization; trajectory l6 event log append read crystallize",
			Resources: []core.ResourceRef{
				{Type: core.CapabilityAPI, Name: "AppendTrajectory", Ref: "api:AppendTrajectory",
					Desc:   "Append one event under key: a turn topic id (empty node_path) or a plan id with the dotted node_path of the step it binds to (a missing node is created pending). EventType is free for a turn event; plan-bound events must use the plan vocabulary (plan_step, llm_request, llm_output, tool_call, tool_result, subagent_spawn, subagent_done, context_inject, ask_user, user_reply). A payload over 4 KiB is refused, not truncated; Seq and the plan fields are assigned by the library",
					Input:  `{"type":"object","properties":{"session_id":{"type":"string","description":"Turn topic id or plan id (16-char hex)"},"node_path":{"type":"string","description":"Dotted plan node path for a plan-bound event; empty for a turn event"},"event_type":{"type":"string"},"payload":{"type":"string","description":"Up to 4 KiB"},"timestamp":{"type":"integer","description":"unix ms"}},"required":["session_id","event_type","timestamp"]}`,
					Output: "ok"},
				{Type: core.CapabilityAPI, Name: "ReadTrajectory", Ref: "api:ReadTrajectory",
					Desc:   "Read one key's events in Seq order; the turn's topic id (Search minted it) reads that turn, and a plan id reads every event bound to that plan",
					Input:  `{"type":"object","properties":{"session_id":{"type":"string","description":"Turn topic id or plan id (16-char hex)"}},"required":["session_id"]}`,
					Output: "List of TrajectorySlot"},
				{Type: core.CapabilityAPI, Name: "ListTrajectorySessions", Ref: "api:ListTrajectorySessions",
					Desc:   "Summarize every trajectory key of the domain — turn topic ids and plan ids — with step count and last-append time; the ids feed ReadTrajectory and Crystallize directly",
					Input:  `{"type":"object","properties":{}}`,
					Output: "List of TrajectorySessionSummary (session_id/steps/last_append_at)"},
				{Type: core.CapabilityAPI, Name: "Crystallize", Ref: "api:Crystallize",
					Desc:   "Distil one trajectory key's L6 events into capability drafts; pass the turn's topic id (the new_topic_id Search returned) for a single turn, or a plan id to aggregate every event bound to that plan. Not on the interactive hot path; the product is a draft and must be promoted via UpdateCapability (status=active) before use",
					Input:  `{"type":"object","properties":{"session_id":{"type":"string","description":"Trajectory key: the turn's topic id or a plan id (16-char hex)"}},"required":["session_id"]}`,
					Output: "CrystallizeResult (CreatedIDs/ReusedIDs/MergedIDs/Errors/Details)"},
			},
		},
		{
			Name: "memhop-plan", Version: "4",
			Summary: "L6 plan-tree manual: the host's task tree lives in MemHop, addressed by a plan id the host derives from a name (api.NewPlanID) — deterministic, so a restart recovers the tree by naming the plan again. Nodes are addressed by dotted paths (\"1\", \"1.2\"). Go host only: these methods are deliberately not MCP tools — they belong to the stateful host loop",
			Trigger: "host task tree, plan sync commit state, recover a plan after restart; plan l6 tree sync commit state nodepath",
			Resources: []core.ResourceRef{
				{Type: core.CapabilityAPI, Name: "SyncPlanTree", Ref: "api:SyncPlanTree",
					Desc:   "Push the host's authoritative whole tree: adds/updates nodes by path, deletes vanished nodes with their bound events, never appends plan_step. Blank Title/Type/Status/Summary inherit stored values, so a partial snapshot never rewinds a done step. A nil root wipes the plan (nodes and events) and keeps the planID for the next task; seed the fresh tree with a single-node sync (empty status lands pending)",
					Input:  `{"type":"object","properties":{"plan_id":{"type":"string","description":"Plan id (16-char hex)"},"root":{"type":"object","description":"Whole-tree snapshot; nil wipes the plan","properties":{"node_path":{"type":"string"},"title":{"type":"string"},"type":{"type":"string"},"status":{"type":"string"},"summary":{"type":"string"},"children":{"type":"array","items":{"type":"object"}}}}},"required":["plan_id"]}`,
					Output: "ok"},
				{Type: core.CapabilityAPI, Name: "PlanCommit", Ref: "api:PlanCommit",
					Desc:   "Advance one node to a status (pending|in_progress|running|done|failed), append its step event (plan vocabulary enforced) and roll done children's summaries up into it. An unknown status or event type is refused before anything is written",
					Input:  `{"type":"object","properties":{"plan_id":{"type":"string","description":"Plan id (16-char hex)"},"node_path":{"type":"string","description":"Dotted path, e.g. \"1.2\""},"event_type":{"type":"string"},"summary":{"type":"string","description":"The node's own conclusion, kept on later syncs"},"status":{"type":"string","description":"pending|in_progress|running|done|failed"},"timestamp":{"type":"integer"}},"required":["plan_id","node_path","status"]}`,
					Output: "ok"},
				{Type: core.CapabilityAPI, Name: "PlanState", Ref: "api:PlanState",
					Desc:   "Read the plan forest (roots plus done/total counts) with string statuses — the restart-recovery read",
					Input:  `{"type":"object","properties":{"plan_id":{"type":"string","description":"Plan id (16-char hex)"}},"required":["plan_id"]}`,
					Output: "PlanTree (Roots/DoneCount/TotalCount)"},
			},
		},
	}
}
