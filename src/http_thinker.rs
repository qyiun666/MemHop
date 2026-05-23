//! HttpThinker — concrete Thinker implementation using reqwest (blocking).
//!
//! Calls any OpenAI-compatible Chat Completions API. Three tiers:
//! - `think_fast`: uses `fast_model`, non-streaming
//! - `think_deep`: uses `model`, non-streaming
//! - `think_stream`: uses `model`, SSE streaming, each token pushed to callback

use std::sync::Arc;

use pyo3::prelude::*;
use reqwest::blocking::Client;

use crate::thinker::Thinker;
use crate::types::BrainError;

// ── HttpThinker ──────────────────────────────────────────

/// An OpenAI-compatible HTTP LLM provider.
///
/// Constructed with an endpoint URL, API key, and model names.
/// Uses `reqwest::blocking` for all requests.
#[pyclass(name = "HttpThinker")]
#[derive(Clone)]
pub struct HttpThinker {
    /// API endpoint URL (e.g. https://api.openai.com/v1/chat/completions)
    #[pyo3(get, set)]
    pub endpoint: String,
    /// API key / bearer token
    #[pyo3(get, set)]
    pub api_key: String,
    /// Primary model name (used for deep reasoning + streaming)
    #[pyo3(get, set)]
    pub model: String,
    /// Fast/cheap model name (used for fast reasoning)
    #[pyo3(get, set)]
    pub fast_model: String,
    /// Shared HTTP client (Arc so HttpThinker is Clone)
    pub client: Arc<Client>,
}

#[pymethods]
impl HttpThinker {
    #[new]
    #[pyo3(signature = (
        endpoint = "https://api.openai.com/v1/chat/completions",
        api_key = "",
        model = "gpt-4o",
        fast_model = "gpt-4o-mini",
    ))]
    pub fn new(endpoint: &str, api_key: &str, model: &str, fast_model: &str) -> Self {
        HttpThinker {
            endpoint: endpoint.to_string(),
            api_key: api_key.to_string(),
            model: model.to_string(),
            fast_model: fast_model.to_string(),
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
            "HttpThinker(endpoint='{}', model='{}', fast_model='{}')",
            self.endpoint, self.model, self.fast_model
        )
    }
}

impl HttpThinker {
    /// Build the required request headers.
    fn headers(&self) -> reqwest::header::HeaderMap {
        use reqwest::header::{AUTHORIZATION, CONTENT_TYPE};
        let mut headers = reqwest::header::HeaderMap::new();
        headers.insert(CONTENT_TYPE, "application/json".parse().unwrap());
        if !self.api_key.is_empty() {
            let bearer = format!("Bearer {}", self.api_key);
            headers.insert(AUTHORIZATION, bearer.parse().unwrap());
        }
        headers
    }

    /// Build the JSON body bytes for a Chat Completions request.
    fn build_body_bytes(&self, prompt: &str, model: &str, stream: bool) -> Vec<u8> {
        let body = serde_json::json!({
            "model": model,
            "messages": [{"role": "user", "content": prompt}],
            "stream": stream,
        });
        serde_json::to_vec(&body).unwrap_or_default()
    }

    /// Call the API with a non-streaming request and extract the response text.
    fn call_non_streaming(&self, prompt: &str, model: &str) -> Result<String, BrainError> {
        let body_bytes = self.build_body_bytes(prompt, model, false);
        let resp = self
            .client
            .post(&self.endpoint)
            .headers(self.headers())
            .body(body_bytes)
            .send()
            .map_err(|e| BrainError::ThinkerFailed(format!("HTTP error: {}", e)))?;

        let status = resp.status();
        if !status.is_success() {
            let text = resp.text().unwrap_or_default();
            return Err(BrainError::ThinkerFailed(format!(
                "API returned {}: {}",
                status, text
            )));
        }

        let body_text = resp
            .text()
            .map_err(|e| BrainError::ThinkerFailed(format!("read error: {}", e)))?;

        let json: serde_json::Value =
            serde_json::from_str(&body_text)
                .map_err(|e| BrainError::ThinkerFailed(format!("JSON parse error: {}", e)))?;

        let content = json["choices"][0]["message"]["content"]
            .as_str()
            .ok_or_else(|| BrainError::ThinkerFailed("no content in response".into()))?;

        Ok(content.to_string())
    }
}

impl Thinker for HttpThinker {
    fn think_fast(&self, prompt: &str) -> Result<String, BrainError> {
        self.call_non_streaming(prompt, &self.fast_model)
    }

    fn think_deep(&self, prompt: &str) -> Result<String, BrainError> {
        self.call_non_streaming(prompt, &self.model)
    }

    fn think_stream(
        &self,
        prompt: &str,
        on_chunk: &mut dyn FnMut(&str),
    ) -> Result<String, BrainError> {
        let body_bytes = self.build_body_bytes(prompt, &self.model, true);
        let resp = self
            .client
            .post(&self.endpoint)
            .headers(self.headers())
            .body(body_bytes)
            .send()
            .map_err(|e| BrainError::ThinkerFailed(format!("HTTP error: {}", e)))?;

        let status = resp.status();
        if !status.is_success() {
            let text = resp.text().unwrap_or_default();
            return Err(BrainError::ThinkerFailed(format!(
                "API returned {}: {}",
                status, text
            )));
        }

        // Read SSE response line by line using read_line to avoid type inference issues
        use std::io::{BufRead, BufReader};
        let mut reader = BufReader::new(resp);
        let mut line_buf = String::new();
        let mut full = String::new();

        loop {
            line_buf.clear();
            let n = reader
                .read_line(&mut line_buf)
                .map_err(|e| BrainError::ThinkerFailed(format!("read error: {}", e)))?;
            if n == 0 {
                break;
            }

            let line = line_buf.trim();
            if let Some(data) = line.strip_prefix("data: ") {
                let data = data.trim();
                if data == "[DONE]" {
                    break;
                }

                if let Ok(parsed) = serde_json::from_str::<serde_json::Value>(data) {
                    if let Some(text) = parsed["choices"][0]["delta"]["content"].as_str() {
                        on_chunk(text);
                        full.push_str(text);
                    }
                }
            }
        }

        Ok(full)
    }
}
