use crate::engine::EngineInner;
use crate::error::{MemHopError, Result};
use crate::types::DomainTree;

impl EngineInner {
    pub fn create_tree(&mut self, name: &str) -> Result<()> {
        self.check_closed()?;
        if self.trees.contains_key(name) {
            return Err(MemHopError::InvalidArgument(format!("tree '{}' already exists", name)));
        }
        let tree = DomainTree::create(&self.storage_path, name)?;
        self.trees.insert(name.to_string(), tree);
        Ok(())
    }

    pub fn remove_tree(&mut self, name: &str) -> Result<()> {
        self.check_closed()?;
        if name == self.default_tree {
            return Err(MemHopError::InvalidArgument("cannot remove the default tree".into()));
        }
        self.trees.remove(name).ok_or_else(|| MemHopError::NotFound(format!("tree '{}' not found", name)))?;
        Ok(())
    }

    pub fn list_trees(&self) -> Vec<String> {
        self.trees.keys().cloned().collect()
    }
}
