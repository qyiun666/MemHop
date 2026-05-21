use pyo3::prelude::*;

mod types;
mod engine;
mod encoder;
mod hopfield;
mod storage;
mod index;
mod python_conv;
mod meta_index;
mod recall_strategies;

#[pymodule]
fn _core(m: &Bound<'_, PyModule>) -> PyResult<()> {
    m.add_class::<types::PyMemory>()?;
    m.add_class::<engine::MemHopEngine>()?;
    m.add("MemHopError", m.py().get_type::<types::MemHopError>())?;
    m.add("MemHopClosedError", m.py().get_type::<types::MemHopClosedError>())?;
    Ok(())
}