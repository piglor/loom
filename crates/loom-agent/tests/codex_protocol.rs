use anyhow::Result;
use loom_agent::codex::Codex;
use serde_json::json;
use std::{fs, os::unix::fs::PermissionsExt, time::Duration};

#[test]
fn exact_thread_and_early_completion_survive_protocol_ordering() -> Result<()> {
    let directory = tempfile::tempdir()?;
    let binary = directory.path().join("fake-codex");
    fs::write(&binary, include_str!("fixtures/fake_codex.py"))?;
    fs::set_permissions(&binary, fs::Permissions::from_mode(0o700))?;
    let mut runtime = Codex::start(&binary, directory.path(), Duration::from_secs(5))?;
    let id = runtime.open_thread(directory.path(), None)?;
    let output = runtime.turn(&id, "hello", json!({"type":"object"}))?;
    assert_eq!(output.text, "{\"ok\":true}");
    runtime.stop()?;
    let mut resumed = Codex::start(&binary, directory.path(), Duration::from_secs(5))?;
    assert_eq!(resumed.open_thread(directory.path(), Some(&id))?, id);
    assert!(resumed.turn(&id, "approval", json!({})).is_err());
    resumed.stop()?;
    Ok(())
}
