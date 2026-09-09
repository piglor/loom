use anyhow::Result;
use loom_agent::codex::Codex;
use serde_json::json;
use std::{path::PathBuf, time::Duration};

fn fixture_binary() -> PathBuf {
    // Execute an immutable checked-in fixture. Writing executable fixtures in
    // parallel with fork/exec can leave a transient inherited writer in another
    // child and make Linux reject execution with ETXTBSY, even after local close.
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/fake_codex.py")
}

#[test]
fn model_turn_requires_persisted_binding() -> Result<()> {
    let directory = tempfile::tempdir()?;
    let binary = fixture_binary();
    let mut runtime = Codex::start(&binary, directory.path(), Duration::from_secs(5))?;
    assert!(runtime.turn("thread-fixture", "hello", json!({})).is_err());
    assert!(
        runtime
            .open_bound_thread(directory.path(), None, |_| anyhow::bail!(
                "Persistence unavailable"
            ))
            .is_err()
    );
    assert!(runtime.turn("thread-fixture", "hello", json!({})).is_err());
    runtime.stop()?;
    Ok(())
}

#[test]
fn exact_thread_and_early_completion_survive_protocol_ordering() -> Result<()> {
    let directory = tempfile::tempdir()?;
    let binary = fixture_binary();
    let mut runtime = Codex::start(&binary, directory.path(), Duration::from_secs(5))?;
    let id = runtime.open_bound_thread(directory.path(), None, |id| {
        assert_eq!(id, "thread-fixture");
        Ok(())
    })?;
    assert!(
        runtime
            .turn("unrelated-thread", "hello", json!({}))
            .is_err()
    );
    let output = runtime.turn(&id, "hello", json!({"type":"object"}))?;
    assert_eq!(output.text, "{\"ok\":true}");
    assert!(
        runtime
            .turn(&id, "second turn is not a retry", json!({}))
            .is_err()
    );
    runtime.stop()?;
    let mut resumed = Codex::start(&binary, directory.path(), Duration::from_secs(5))?;
    assert_eq!(
        resumed.open_bound_thread(directory.path(), Some(&id), |_| Ok(()))?,
        id
    );
    assert!(resumed.turn(&id, "approval", json!({})).is_err());
    assert!(
        resumed
            .turn(&id, "retry after ambiguous request", json!({}))
            .is_err()
    );
    resumed.stop()?;
    Ok(())
}

#[test]
fn wrong_resumed_context_never_reaches_persistence_or_inference() -> Result<()> {
    let directory = tempfile::tempdir()?;
    let binary = fixture_binary();
    let mut runtime = Codex::start(&binary, directory.path(), Duration::from_secs(5))?;
    assert!(
        runtime
            .open_bound_thread(directory.path(), Some("wrong-resume"), |_| panic!(
                "Wrong context must not be persisted"
            ))
            .is_err()
    );
    assert!(
        runtime
            .turn("unrelated-context", "hello", json!({}))
            .is_err()
    );
    runtime.stop()?;
    Ok(())
}

#[test]
fn oversized_turn_cannot_be_retried_and_stopped_runtime_cannot_reopen() -> Result<()> {
    let directory = tempfile::tempdir()?;
    let binary = fixture_binary();
    let mut runtime = Codex::start(&binary, directory.path(), Duration::from_secs(5))?;
    let id = runtime.open_bound_thread(directory.path(), None, |_| Ok(()))?;
    assert!(runtime.turn(&id, &"x".repeat(65536), json!({})).is_err());
    assert!(runtime.turn(&id, "hidden retry", json!({})).is_err());
    runtime.stop()?;
    assert!(
        runtime
            .open_bound_thread(directory.path(), Some(&id), |_| Ok(()))
            .is_err()
    );
    Ok(())
}

#[test]
fn lost_turn_acknowledgment_never_allows_relaunch() -> Result<()> {
    let directory = tempfile::tempdir()?;
    let binary = fixture_binary();
    let mut runtime = Codex::start(&binary, directory.path(), Duration::from_secs(5))?;
    let id = runtime.open_bound_thread(directory.path(), None, |_| Ok(()))?;
    assert!(runtime.turn(&id, "lost-ack", json!({})).is_err());
    assert!(runtime.turn(&id, "retry", json!({})).is_err());
    runtime.stop()?;
    Ok(())
}

#[test]
fn opaque_unicode_thread_ids_follow_the_server_contract() -> Result<()> {
    let directory = tempfile::tempdir()?;
    let binary = fixture_binary();
    for id in [
        "é".repeat(256),
        "é".repeat(257),
        "bad\0value".into(),
        "bad\u{7f}value".into(),
    ] {
        let mut runtime = Codex::start(&binary, directory.path(), Duration::from_secs(5))?;
        let result = runtime.open_bound_thread(directory.path(), Some(&id), |_| Ok(()));
        assert_eq!(result.is_ok(), id == "é".repeat(256));
        runtime.stop()?;
    }
    Ok(())
}
