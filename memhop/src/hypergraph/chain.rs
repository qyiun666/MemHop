use crate::engram::Hyperedge;
use crate::lmdb::L1Env;
use crate::error::{Result, MemHopError};

#[allow(dead_code)]
pub fn chain_forward(env: &L1Env, txn: &heed::RoTxn<'_>, start_id: &str, max_len: usize) -> Result<Vec<Hyperedge>> {
    let mut result = Vec::new();
    let mut current_id = Some(start_id.to_string());
    while let Some(ref cid) = current_id {
        if result.len() >= max_len { break; }
        match env.hyperedges.get(txn, cid).map_err(|e| MemHopError::Storage(e.to_string()))? {
            Some(bytes) => {
                let he: Hyperedge = bincode::deserialize(bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
                current_id = he.chain_next.clone();
                result.push(he);
            }
            None => break,
        }
    }
    Ok(result)
}

#[allow(dead_code)]
pub fn chain_backward(env: &L1Env, txn: &heed::RoTxn<'_>, start_id: &str, max_len: usize) -> Result<Vec<Hyperedge>> {
    let mut result = Vec::new();
    let mut current_id = Some(start_id.to_string());
    while let Some(ref cid) = current_id {
        if result.len() >= max_len { break; }
        match env.hyperedges.get(txn, cid).map_err(|e| MemHopError::Storage(e.to_string()))? {
            Some(bytes) => {
                let he: Hyperedge = bincode::deserialize(bytes).map_err(|e| MemHopError::Storage(e.to_string()))?;
                current_id = he.chain_prev.clone();
                result.push(he);
            }
            None => break,
        }
    }
    Ok(result)
}

#[allow(dead_code)]
pub fn chain_both(env: &L1Env, txn: &heed::RoTxn<'_>, start_id: &str, max_each: usize) -> Result<Vec<Hyperedge>> {
    let forward = chain_forward(env, txn, start_id, max_each)?;
    let mut backward = chain_backward(env, txn, start_id, max_each)?;
    backward.reverse();
    backward.extend(forward);
    Ok(backward)
}
