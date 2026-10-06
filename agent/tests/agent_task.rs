//! The on-device agent loop against a fake OpenAI-compatible server that
//! behaves like `litert-lm serve`: first a tool call, then a final answer.

use std::sync::{Arc, Mutex};

use cami_agent::agent_task::{run_agent_task, ToolContext};
use serde_json::Value;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

/// Reads one HTTP request (headers plus a Content-Length body) and returns the body.
async fn read_request(socket: &mut TcpStream) -> String {
    let mut buf = Vec::new();
    let mut chunk = [0u8; 4096];
    loop {
        let n = socket.read(&mut chunk).await.unwrap();
        if n == 0 {
            break;
        }
        buf.extend_from_slice(&chunk[..n]);
        if let Some(end) = buf.windows(4).position(|w| w == b"\r\n\r\n") {
            let head = String::from_utf8_lossy(&buf[..end]).to_lowercase();
            let len: usize = head
                .lines()
                .find_map(|l| l.strip_prefix("content-length:"))
                .and_then(|v| v.trim().parse().ok())
                .unwrap_or(0);
            while buf.len() < end + 4 + len {
                let n = socket.read(&mut chunk).await.unwrap();
                if n == 0 {
                    break;
                }
                buf.extend_from_slice(&chunk[..n]);
            }
            return String::from_utf8_lossy(&buf[end + 4..]).into_owned();
        }
    }
    String::new()
}

/// Serves the given JSON replies in order, one per request, and records each
/// request body.
async fn fake_model_server(replies: Vec<&'static str>) -> (String, Arc<Mutex<Vec<String>>>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let bodies = Arc::new(Mutex::new(Vec::new()));
    let seen = bodies.clone();
    tokio::spawn(async move {
        for reply in replies {
            let (mut socket, _) = listener.accept().await.unwrap();
            let body = read_request(&mut socket).await;
            seen.lock().unwrap().push(body);
            let resp = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                reply.len(),
                reply
            );
            socket.write_all(resp.as_bytes()).await.unwrap();
            let _ = socket.shutdown().await;
        }
    });
    (format!("http://{addr}"), bodies)
}

fn ctx() -> ToolContext {
    ToolContext {
        cache_dir: std::env::temp_dir(),
        model_id: "gemma-4-e4b".into(),
        backend: "litert".into(),
    }
}

// The exact shape `litert-lm serve` returns for a tool call (non-streaming):
// arguments is a JSON string, finish_reason is "tool_calls".
const TOOL_CALL: &str = r#"{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1_0","type":"function","function":{"name":"device_status","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}"#;
const FINAL: &str = r#"{"id":"chatcmpl_2","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"This device has been up for 3 hours and has plenty of free memory."},"finish_reason":"stop"}]}"#;

#[tokio::test]
async fn test_agent_calls_a_tool_then_answers() {
    let (url, bodies) = fake_model_server(vec![TOOL_CALL, FINAL]).await;

    let out = run_agent_task(&url, "/models/sha256-abc,cpu", "How is this device doing?", 4, &ctx()).await;

    assert_eq!(out.status, "done", "error: {}", out.error);
    assert_eq!(
        out.answer,
        "This device has been up for 3 hours and has plenty of free memory."
    );
    assert_eq!(out.steps.len(), 1);
    assert_eq!(out.steps[0].tool, "device_status");
    let status: Value = serde_json::from_str(&out.steps[0].result).unwrap();
    assert_eq!(status["arch"], std::env::consts::ARCH);

    let bodies = bodies.lock().unwrap();
    assert_eq!(bodies.len(), 2);
    let first: Value = serde_json::from_str(&bodies[0]).unwrap();
    assert_eq!(first["model"], "/models/sha256-abc,cpu");
    assert_eq!(first["tools"].as_array().unwrap().len(), 3);

    // The second request carries the assistant's tool call and the tool's result.
    let second: Value = serde_json::from_str(&bodies[1]).unwrap();
    let msgs = second["messages"].as_array().unwrap();
    let assistant = &msgs[msgs.len() - 2];
    let tool = &msgs[msgs.len() - 1];
    assert_eq!(assistant["tool_calls"][0]["id"], "call_1_0");
    assert_eq!(tool["role"], "tool");
    assert_eq!(tool["tool_call_id"], "call_1_0");
    assert!(tool["content"].as_str().unwrap().contains("mem_total_mb"));
}

#[tokio::test]
async fn test_last_round_offers_no_tools() {
    // With max_steps = 1 the model must answer straight away.
    let (url, bodies) = fake_model_server(vec![FINAL]).await;
    let out = run_agent_task(&url, "m", "hi", 1, &ctx()).await;
    assert_eq!(out.status, "done");
    let first: Value = serde_json::from_str(&bodies.lock().unwrap()[0]).unwrap();
    assert!(first.get("tools").is_none());
}

#[tokio::test]
async fn test_unreachable_model_server_fails_cleanly() {
    let out = run_agent_task("http://127.0.0.1:9", "m", "hi", 4, &ctx()).await;
    assert_eq!(out.status, "failed");
    assert!(out.error.contains("model request failed"), "{}", out.error);
    assert!(out.answer.is_empty());
}
