//! HttpCalibrator — concrete Calibrator implementation using reqwest (blocking).
//!
//! Calls any OpenAI-compatible Chat Completions API for calibration tasks.
//! Shares the same pattern as HttpThinker but is specialised for memory
//! maintenance: importance scoring, semantic dedup, and link validation.
//!
//! Supports Ollama (no api_key) and standard OpenAI-compatible endpoints.

use std::fmt;
use std::sync::Arc;

use pyo3::prelude::*;
use reqwest::blocking::Client;

use crate::calibrator::{Calibrator, CalibrationContext, DedupResult, LinkValidation};
use crate::types::BrainError;

// ── HttpCalibrator ───────────────────────────────────────

/// An OpenAI-compatible HTTP calibrator provider.
///
/// Constructed with an endpoint URL, optional API key, and model name.
/// Unlike HttpThinker (which is `Clone`), this is also Clone so
/// it can be passed into BrainLoop.
#[pyclass(name = "HttpCalibrator")]
#[derive(Clone)]
pub struct HttpCalibrator {
    /// API endpoint URL (e.g. https://api.openai.com/v1/chat/completions)
    #[pyo3(get, set)]
    pub endpoint: String,
    /// API key / bearer token (None = Ollama, no auth header sent)
    #[pyo3(get, set)]
    pub api_key: Option<String>,
    /// Model name for calibration (e.g. "qwen2.5:0.5b")
    #[pyo3(get, set)]
    pub model: String,
    /// Shared HTTP client (Arc so HttpCalibrator is Clone)
    pub client: Arc<Client>,
}

/// Manual Debug impl because Arc<Client> does not implement Debug.
impl fmt::Debug for HttpCalibrator {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("HttpCalibrator")
            .field("endpoint", &self.endpoint)
            .field("model", &self.model)
            .field("has_api_key", &self.api_key.is_some())
            .finish()
    }
}

#[pymethods]
impl HttpCalibrator {
    #[new]
    #[pyo3(signature = (
        endpoint = "http://localhost:11434/v1/chat/completions",
        api_key = None,
        model = "qwen2.5:0.5b",
    ))]
    pub fn new(endpoint: &str, api_key: Option<String>, model: &str) -> Self {
        HttpCalibrator {
            endpoint: endpoint.to_string(),
            api_key: api_key.filter(|k| !k.is_empty()),
            model: model.to_string(),
            client: Arc::new(
                Client::builder()
                    .timeout(std::time::Duration::from_secs(120))
                    .build()
                    .expect("failed to build reqwest blocking client"),
            ),
        }
    }

    fn __repr__(&self) -> String {
        format!(
            "HttpCalibrator(endpoint='{}', model='{}')",
            self.endpoint, self.model
        )
    }
}

impl HttpCalibrator {
    /// Build the required request headers.
    fn headers(&self) -> reqwest::header::HeaderMap {
        use reqwest::header::{AUTHORIZATION, CONTENT_TYPE};
        let mut headers = reqwest::header::HeaderMap::new();
        headers.insert(CONTENT_TYPE, "application/json".parse().unwrap());
        if let Some(ref key) = self.api_key {
            let bearer = format!("Bearer {}", key);
            headers.insert(AUTHORIZATION, bearer.parse().unwrap());
        }
        headers
    }

    /// Build the JSON body bytes for a Chat Completions request.
    fn build_body_bytes(&self, prompt: &str, stream: bool) -> Vec<u8> {
        let body = serde_json::json!({
            "model": self.model,
            "messages": [{"role": "user", "content": prompt}],
            "stream": stream,
        });
        serde_json::to_vec(&body).unwrap_or_default()
    }

    /// Single non-streaming API call.
    fn call(&self, prompt: &str) -> Result<String, BrainError> {
        let body_bytes = self.build_body_bytes(prompt, false);
        let resp = self
            .client
            .post(&self.endpoint)
            .headers(self.headers())
            .body(body_bytes)
            .send()
            .map_err(|e| BrainError::CalibratorFailed(format!("HTTP error: {}", e)))?;

        let status = resp.status();
        if !status.is_success() {
            let text = resp.text().unwrap_or_default();
            return Err(BrainError::CalibratorFailed(format!(
                "API returned {}: {}",
                status, text
            )));
        }

        let body_text = resp
            .text()
            .map_err(|e| BrainError::CalibratorFailed(format!("read error: {}", e)))?;

        let json: serde_json::Value = serde_json::from_str(&body_text)
            .map_err(|e| BrainError::CalibratorFailed(format!("JSON parse error: {}", e)))?;

        let content = json["choices"][0]["message"]["content"]
            .as_str()
            .ok_or_else(|| BrainError::CalibratorFailed("no content in response".into()))?;

        Ok(content.to_string())
    }
}

impl Calibrator for HttpCalibrator {
    fn cal_importance(
        &self,
        text: &str,
        context: &CalibrationContext,
    ) -> Result<f32, BrainError> {
        let prompt = format!(
            "Evaluate the importance of the following memory. \
             Return ONLY a number between 0.0 and 1.0, nothing else.\n\
             Memory: {text}\n\
             Domain: {domain}\n\
             Layer: {layer}",
            text = text,
            domain = context.domain.as_deref().unwrap_or("unknown"),
            layer = context.layer.as_deref().unwrap_or("unknown"),
        );
        let response = self.call(&prompt)?;
        response
            .trim()
            .parse::<f32>()
            .map_err(|_| BrainError::ParseError)
    }

    fn cal_dedup(
        &self,
        text_a: &str,
        text_b: &str,
    ) -> Result<DedupResult, BrainError> {
        let prompt = format!(
            "Are the following two memories semantically duplicate? \
             Return ONLY \"true\" or \"false\".\n\
             Memory A: {a}\nMemory B: {b}",
            a = text_a,
            b = text_b,
        );
        let response = self.call(&prompt)?;
        let is_dup = response.trim().eq_ignore_ascii_case("true");
        Ok(DedupResult {
            is_duplicate: is_dup,
            confidence: if is_dup { 0.8 } else { 0.9 },
            merge_suggestion: None,
        })
    }

    fn cal_link(
        &self,
        from_text: &str,
        to_text: &str,
        relation: &str,
    ) -> Result<LinkValidation, BrainError> {
        let prompt = format!(
            "Is the following link semantically valid? \
             Return ONLY \"true\" or \"false\".\n\
             From: {from}\nTo: {to}\nRelation: {rel}",
            from = from_text,
            to = to_text,
            rel = relation,
        );
        let response = self.call(&prompt)?;
        let valid = response.trim().eq_ignore_ascii_case("true");
        Ok(LinkValidation {
            is_valid: valid,
            confidence: if valid { 0.8 } else { 0.9 },
        })
    }

    fn cal_batch_importance(
        &self,
        items: &[(String, CalibrationContext)],
    ) -> Result<Vec<f32>, BrainError> {
        items
            .iter()
            .map(|(text, ctx)| self.cal_importance(text, ctx))
            .collect()
    }
}

// ── Tests ─────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_http_calibrator_construct_default() {
        let cal = HttpCalibrator::new(
            "http://localhost:11434/v1/chat/completions",
            None,
            "qwen2.5:0.5b",
        );
        assert_eq!(cal.endpoint, "http://localhost:11434/v1/chat/completions");
        assert!(cal.api_key.is_none());
        assert_eq!(cal.model, "qwen2.5:0.5b");
    }

    #[test]
    fn test_http_calibrator_empty_api_key_becomes_none() {
        let cal = HttpCalibrator::new("http://localhost:11434/v1", Some("".into()), "m");
        assert!(cal.api_key.is_none());
    }

    #[test]
    fn test_http_calibrator_with_key() {
        let cal = HttpCalibrator::new(
            "https://api.openai.com/v1/chat/completions",
            Some("sk-test".into()),
            "gpt-4o-mini",
        );
        assert_eq!(cal.api_key, Some("sk-test".into()));
    }

    #[test]
    fn test_build_body_bytes_format() {
        let cal = HttpCalibrator::new("http://localhost:11434/v1", None, "qwen");
        let bytes = cal.build_body_bytes("say hello", false);
        let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(json["model"], "qwen");
        assert_eq!(json["messages"][0]["role"], "user");
        assert_eq!(json["messages"][0]["content"], "say hello");
        assert_eq!(json["stream"], false);
    }

    #[test]
    fn test_headers_include_content_type() {
        let cal = HttpCalibrator::new("http://localhost:11434/v1", None, "m");
        let headers = cal.headers();
        assert_eq!(
            headers.get("Content-Type").unwrap().to_str().unwrap(),
            "application/json"
        );
    }

    #[test]
    fn test_headers_no_auth_without_key() {
        let cal = HttpCalibrator::new("http://localhost:11434/v1", None, "m");
        let headers = cal.headers();
        assert!(headers.get("Authorization").is_none());
    }

    #[test]
    fn test_headers_include_auth_with_key() {
        let cal = HttpCalibrator::new(
            "https://api.openai.com/v1/chat/completions",
            Some("sk-test".into()),
            "gpt-4o-mini",
        );
        let headers = cal.headers();
        assert_eq!(
            headers.get("Authorization").unwrap().to_str().unwrap(),
            "Bearer sk-test"
        );
    }

    #[test]
    fn test_http_calibrator_repr() {
        let cal = HttpCalibrator::new("http://localhost:11434/v1", None, "qwen");
        let repr = cal.__repr__();
        assert!(repr.contains("qwen"));
        assert!(repr.contains("HttpCalibrator"));
    }
}
