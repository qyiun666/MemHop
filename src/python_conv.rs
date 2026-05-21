/// Python ↔ JSON value conversion helpers.
///
/// Extracted from engine.rs for modularity. Uses PyDict::new with pre-allocation
/// and avoids unnecessary intermediate allocations.

use pyo3::prelude::*;
use pyo3::IntoPyObjectExt;
use pyo3::types::PyDict;
use std::collections::HashMap;

/// Convert a single Bound<'_, PyAny> to serde_json::Value.
pub(crate) fn bound_to_json_value(val: &Bound<'_, PyAny>) -> serde_json::Value {
    if val.is_none() {
        return serde_json::Value::Null;
    }
    if let Ok(b) = val.extract::<bool>() {
        return serde_json::Value::Bool(b);
    }
    if let Ok(i) = val.extract::<i64>() {
        return serde_json::Value::Number(i.into());
    }
    if let Ok(f) = val.extract::<f64>() {
        return serde_json::Number::from_f64(f)
            .map(serde_json::Value::Number)
            .unwrap_or(serde_json::Value::Null);
    }
    if let Ok(s) = val.extract::<String>() {
        return serde_json::Value::String(s);
    }
    let py = val.py();
    if let Ok(list) = val.extract::<Vec<PyObject>>() {
        let arr: Vec<serde_json::Value> = list
            .iter()
            .map(|item| bound_to_json_value(item.bind(py)))
            .collect();
        return serde_json::Value::Array(arr);
    }
    if let Ok(dict_map) = val.extract::<HashMap<String, PyObject>>() {
        let mut map = serde_json::Map::new();
        for (k, v) in &dict_map {
            map.insert(k.clone(), bound_to_json_value(v.bind(py)));
        }
        return serde_json::Value::Object(map);
    }
    serde_json::Value::Null
}

/// Convert a Python dict-like HashMap into a JSON map.
pub(crate) fn pydict_to_json_map(
    meta: &HashMap<String, PyObject>,
    py: Python<'_>,
) -> HashMap<String, serde_json::Value> {
    meta.iter()
        .map(|(k, v)| (k.clone(), bound_to_json_value(v.bind(py))))
        .collect()
}

/// Convert a single serde_json::Value to a Python object.
pub(crate) fn json_value_to_pyobj(py: Python<'_>, val: &serde_json::Value) -> PyObject {
    match val {
        serde_json::Value::Null => py.None(),
        serde_json::Value::Bool(b) => b.into_py_any(py).unwrap(),
        serde_json::Value::Number(n) => {
            if let Some(i) = n.as_i64() {
                i.into_py_any(py).unwrap()
            } else if let Some(f) = n.as_f64() {
                f.into_py_any(py).unwrap()
            } else {
                py.None()
            }
        }
        serde_json::Value::String(s) => s.into_py_any(py).unwrap(),
        serde_json::Value::Array(arr) => {
            let items: Vec<PyObject> = arr.iter().map(|v| json_value_to_pyobj(py, v)).collect();
            items.into_py_any(py).unwrap()
        }
        serde_json::Value::Object(map) => {
            let dict = PyDict::new(py);
            for (k, v) in map {
                dict.set_item(k, json_value_to_pyobj(py, v)).unwrap();
            }
            dict.into()
        }
    }
}

/// Convert a JSON HashMap into a Python-compatible HashMap of PyObjects.
pub(crate) fn json_map_to_pydict(
    py: Python<'_>,
    map: &HashMap<String, serde_json::Value>,
) -> HashMap<String, PyObject> {
    map.iter()
        .map(|(k, v)| (k.clone(), json_value_to_pyobj(py, v)))
        .collect()
}