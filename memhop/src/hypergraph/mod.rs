//! L1 全局超图 — 超图 + 超边链。
//! 存储感知节点、超边、超边链，支持 BM25 检索和 BFS 扩散。

mod chain;
mod graph;

pub use graph::*;
