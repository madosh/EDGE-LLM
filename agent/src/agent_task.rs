//! The on-device AI agent: answers an operator's question with the model the
//! device is running, and can call local tools first through OpenAI-style
//! function calling. `litert-lm serve` and Ollama both expose
//! `/v1/chat/completions` with `tools`, so one loop serves both.
//!
//! The tools are read-only by design: the agent can look at the device (its
//! status, its cached models, the loaded model) but cannot change anything.
//! Every tool call is recorded and sent back with the answer.

use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::path::PathBuf;
use std::time::{Duration, Instant};
use tracing::warn;

use crate::model_runtime::http;
use crate::system;

/// Upper bound on model calls for one task, whatever the operator asks for.
pub const MAX_STEPS_LIMIT: u32 = 8;
const TOOL_RESULT_LIMIT: usize = 4000;
const ANSWER_TOKENS: u32 = 512;
const SYSTEM_PROMPT: &str = "You are the on-device assistant of an edge device managed by Cami Fleet. \
You run locally on this device. Use the tools to look up facts about the device before answering \
questions about it. Answer in a few sentences.";

/// What the tools may know about the agent's surroundings.
#[derive(Debug, Clone)]
pub struct ToolContext {
    pub cache_dir: PathBuf,
    pub model_id: String,
    pub backend: String,
}

/// One tool call made while answering.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Step {
    pub tool: String,
    pub arguments: Value,
    pub result: String,
}

#[derive(Debug, Clone, PartialEq)]
pub struct TaskOutcome {
    /// "done" or "failed".
    pub status: &'static str,
    pub answer: String,
    pub error: String,
    pub steps: Vec<Step>,
    pub duration_ms: u32,
}

impl TaskOutcome {
    fn new(status: &'static str, answer: String, error: String, steps: Vec<Step>, start: Instant) -> Self {
        Self {
            status,
            answer,
            error,
            steps,
            duration_ms: start.elapsed().as_millis().min(u128::from(u32::MAX)) as u32,
        }
    }

    pub fn failed(error: impl Into<String>) -> Self {
        Self::new("failed", String::new(), error.into(), Vec::new(), Instant::now())
    }

    pub fn steps_json(&self) -> String {
        serde_json::to_string(&self.steps).unwrap_or_else(|_| "[]".to_string())
    }
}

/// The tools offered to the model, in OpenAI `tools` format. None take
/// arguments, which keeps them safe and easy for small models to call.
pub fn tool_definitions() -> Value {
    let tool = |name: &str, description: &str| {
        json!({
            "type": "function",
            "function": {
                "name": name,
                "description": description,
                "parameters": { "type": "object", "properties": {} }
            }
        })
    };
    json!([
        tool(
            "device_status",
            "Hostname, CPU architecture, OS, uptime, load average and memory of this device."
        ),
        tool(
            "list_cached_models",
            "The verified model files stored on this device, by SHA-256 digest and size."
        ),
        tool(
            "current_model",
            "The model this device is running now and the runtime serving it."
        ),
    ])
}

/// Runs a tool by name and returns its result as JSON text.
pub fn run_tool(name: &str, ctx: &ToolContext) -> String {
    let value = match name {
        "device_status" => system::device_status(),
        "list_cached_models" => system::cached_models(&ctx.cache_dir),
        "current_model" => json!({ "model_id": ctx.model_id, "backend": ctx.backend }),
        other => json!({ "error": format!("unknown tool: {other}") }),
    };
    value.to_string()
}

#[derive(Deserialize)]
struct ChatResponse {
    #[serde(default)]
    choices: Vec<ChatChoice>,
}

#[derive(Deserialize)]
struct ChatChoice {
    message: ChatMessage,
}

#[derive(Deserialize)]
struct ChatMessage {
    #[serde(default)]
    content: Option<String>,
    #[serde(default)]
    tool_calls: Option<Vec<ToolCall>>,
}

#[derive(Deserialize, Serialize, Clone)]
struct ToolCall {
    #[serde(default)]
    id: String,
    #[serde(rename = "type", default = "function_kind")]
    kind: String,
    function: FunctionCall,
}

#[derive(Deserialize, Serialize, Clone)]
struct FunctionCall {
    name: String,
    /// A JSON string in the OpenAI format; some servers send an object.
    #[serde(default)]
    arguments: Value,
}

fn function_kind() -> String {
    "function".to_string()
}

fn parse_arguments(raw: &Value) -> Value {
    match raw {
        Value::String(s) if s.trim().is_empty() => json!({}),
        Value::String(s) => serde_json::from_str(s).unwrap_or_else(|_| Value::String(s.clone())),
        Value::Null => json!({}),
        other => other.clone(),
    }
}

fn truncate(s: String, limit: usize) -> String {
    if s.chars().count() <= limit {
        s
    } else {
        let mut cut: String = s.chars().take(limit).collect();
        cut.push_str("…(truncated)");
        cut
    }
}

/// Answers `prompt` with the model at `base_url` (an OpenAI-compatible server
/// on the device). Each round either calls tools — their results go back to
/// the model — or ends with the answer. The last round offers no tools, so
/// the model has to answer.
pub async fn run_agent_task(
    base_url: &str,
    model: &str,
    prompt: &str,
    max_steps: u32,
    ctx: &ToolContext,
) -> TaskOutcome {
    let start = Instant::now();
    let url = format!("{}/v1/chat/completions", base_url.trim_end_matches('/'));
    let rounds = max_steps.clamp(1, MAX_STEPS_LIMIT);
    let mut messages = vec![
        json!({ "role": "system", "content": SYSTEM_PROMPT }),
        json!({ "role": "user", "content": prompt }),
    ];
    let mut steps: Vec<Step> = Vec::new();

    for round in 0..rounds {
        let mut body = json!({
            "model": model,
            "messages": messages,
            // LiteRT-LM reads max_completion_tokens; Ollama reads max_tokens.
            "max_completion_tokens": ANSWER_TOKENS,
            "max_tokens": ANSWER_TOKENS,
        });
        if round + 1 < rounds {
            body["tools"] = tool_definitions();
        }

        let resp = match http()
            .post(&url)
            .json(&body)
            .timeout(Duration::from_secs(180))
            .send()
            .await
        {
            Ok(r) => r,
            Err(e) => {
                return TaskOutcome::new(
                    "failed",
                    String::new(),
                    format!("model request failed: {e}"),
                    steps,
                    start,
                )
            }
        };
        if !resp.status().is_success() {
            let status = resp.status();
            let text = resp.text().await.unwrap_or_default();
            let error = format!("model server returned HTTP {status}: {}", truncate(text, 500));
            return TaskOutcome::new("failed", String::new(), error, steps, start);
        }
        let parsed: ChatResponse = match resp.json().await {
            Ok(p) => p,
            Err(e) => {
                return TaskOutcome::new(
                    "failed",
                    String::new(),
                    format!("unreadable model response: {e}"),
                    steps,
                    start,
                )
            }
        };
        let Some(choice) = parsed.choices.into_iter().next() else {
            return TaskOutcome::new(
                "failed",
                String::new(),
                "model returned no choices".into(),
                steps,
                start,
            );
        };

        let calls = choice.message.tool_calls.unwrap_or_default();
        if calls.is_empty() {
            let answer = choice.message.content.unwrap_or_default().trim().to_string();
            if answer.is_empty() {
                return TaskOutcome::new(
                    "failed",
                    String::new(),
                    "model returned an empty answer".into(),
                    steps,
                    start,
                );
            }
            return TaskOutcome::new("done", answer, String::new(), steps, start);
        }

        // Echo the assistant turn, then answer each call. Every call needs an
        // id for the tool message to refer to.
        let calls: Vec<ToolCall> = calls
            .into_iter()
            .enumerate()
            .map(|(i, mut call)| {
                if call.id.is_empty() {
                    call.id = format!("call_{round}_{i}");
                }
                call
            })
            .collect();
        messages.push(json!({
            "role": "assistant",
            "content": choice.message.content,
            "tool_calls": calls,
        }));
        for call in &calls {
            let result = truncate(run_tool(&call.function.name, ctx), TOOL_RESULT_LIMIT);
            steps.push(Step {
                tool: call.function.name.clone(),
                arguments: parse_arguments(&call.function.arguments),
                result: result.clone(),
            });
            messages.push(json!({ "role": "tool", "tool_call_id": call.id, "content": result }));
        }
    }

    warn!("agent task hit its step limit without a final answer");
    TaskOutcome::new(
        "failed",
        String::new(),
        "step limit reached before a final answer".into(),
        steps,
        start,
    )
}

/// The stub backend has no language model: it runs the device_status tool and
/// reports it, clearly labelled, so the task flow works in demos and CI.
pub fn stub_task(prompt: &str, ctx: &ToolContext) -> TaskOutcome {
    let start = Instant::now();
    let result = run_tool("device_status", ctx);
    let answer = format!(
        "Stub agent (this device has no language model loaded). You asked: \"{prompt}\". \
         Device status: {result}"
    );
    let steps = vec![Step {
        tool: "device_status".to_string(),
        arguments: json!({}),
        result,
    }];
    TaskOutcome::new("done", answer, String::new(), steps, start)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ctx() -> ToolContext {
        ToolContext {
            cache_dir: std::env::temp_dir(),
            model_id: "gemma-4-e4b".into(),
            backend: "litert".into(),
        }
    }

    #[test]
    fn test_tool_definitions_are_openai_functions() {
        let defs = tool_definitions();
        let defs = defs.as_array().unwrap();
        assert_eq!(defs.len(), 3);
        for d in defs {
            assert_eq!(d["type"], "function");
            assert!(d["function"]["name"].is_string());
            assert_eq!(d["function"]["parameters"]["type"], "object");
        }
    }

    #[test]
    fn test_run_tool() {
        let v: Value = serde_json::from_str(&run_tool("current_model", &ctx())).unwrap();
        assert_eq!(v["model_id"], "gemma-4-e4b");
        let v: Value = serde_json::from_str(&run_tool("rm_rf", &ctx())).unwrap();
        assert!(v["error"].as_str().unwrap().contains("unknown tool"));
    }

    #[test]
    fn test_parse_arguments() {
        assert_eq!(parse_arguments(&json!("{\"a\":1}")), json!({"a": 1}));
        assert_eq!(parse_arguments(&json!("")), json!({}));
        assert_eq!(parse_arguments(&Value::Null), json!({}));
        assert_eq!(parse_arguments(&json!({"b": 2})), json!({"b": 2}));
    }

    #[test]
    fn test_truncate_is_char_safe() {
        assert_eq!(truncate("héllo".into(), 10), "héllo");
        assert_eq!(truncate("héllo".into(), 2), "hé…(truncated)");
    }

    #[test]
    fn test_stub_task() {
        let out = stub_task("how are you?", &ctx());
        assert_eq!(out.status, "done");
        assert_eq!(out.steps.len(), 1);
        assert_eq!(out.steps[0].tool, "device_status");
        assert!(out.answer.contains("Stub agent"));
        let steps: Value = serde_json::from_str(&out.steps_json()).unwrap();
        assert_eq!(steps[0]["tool"], "device_status");
    }
}
