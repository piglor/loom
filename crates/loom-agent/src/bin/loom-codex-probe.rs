//! Opt-in live provider conformance check; two read-only model turns.
use anyhow::{Context, Result, ensure};
use loom_agent::codex::Codex;
use serde_json::{Value, json};
use std::{
    fs::OpenOptions, io::Write, os::unix::fs::OpenOptionsExt, path::PathBuf, time::Duration,
};

fn main() -> Result<()> {
    let args: Vec<String> = std::env::args().collect();
    ensure!(
        args.len() == 4,
        "Usage: loom-codex-probe CODEX_BINARY ABSOLUTE_WORKSPACE NEW_RECEIPT_FILE"
    );
    let binary = PathBuf::from(&args[1]);
    let workspace = PathBuf::from(&args[2]).canonicalize()?;
    // Persist the exact binding before the first model turn; no secret content.
    let mut receipt = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&args[3])?;
    let schema = json!({"type":"object","properties":{"remembered":{"type":"string"}},
        "required":["remembered"],"additionalProperties":false});
    let nonce = uuid::Uuid::new_v4().to_string();
    let mut runtime = Codex::start(&binary, &workspace, Duration::from_secs(180))?;
    let id = runtime.open_thread(&workspace, None)?;
    writeln!(receipt, "{}", json!({"provider_thread_id":id}))?;
    receipt.sync_all()?;
    let receipt_path = PathBuf::from(&args[3]).canonicalize()?;
    std::fs::File::open(receipt_path.parent().context("Receipt parent required")?)?.sync_all()?;
    let first = runtime.turn(&id, &format!("This is a harmless continuity test. Do not use any tools. Remember this marker: {nonce}. Return it as remembered."), schema.clone())?;
    let output: Value = serde_json::from_str(&first.text)?;
    ensure!(
        output["remembered"] == nonce,
        "First response did not preserve marker"
    );
    runtime.stop()?;
    drop(runtime);
    std::thread::sleep(Duration::from_secs(3));
    let mut runtime = Codex::start(&binary, &workspace, Duration::from_secs(180))?;
    let resumed = runtime.open_thread(&workspace, Some(&id))?;
    let second = runtime.turn(
        &resumed,
        "Return the exact marker from the previous turn as remembered. Do not use any tools.",
        schema,
    )?;
    let output: Value = serde_json::from_str(&second.text).context("Invalid structured output")?;
    ensure!(
        output["remembered"] == nonce,
        "Exact session did not retain context"
    );
    runtime.stop()?;
    writeln!(
        receipt,
        "{}",
        json!({"status":"passed","first_usage":first.usage,"second_usage":second.usage})
    )?;
    receipt.sync_all()?;
    println!(
        "PASS: two read-only turns, process stopped between them, exact provider thread and context retained"
    );
    Ok(())
}
