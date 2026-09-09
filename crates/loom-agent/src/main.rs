//! Outbound-only finite runtime worker. Ambiguous launch is never replayed.

use anyhow::{Context, Result, bail, ensure};
use reqwest::{Method, blocking::Client, redirect::Policy};
use rusqlite::{Connection, params};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::{Value, json};
use std::{
    fs::{self, File, OpenOptions},
    io::Read,
    os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    thread,
    time::{Duration, Instant},
};
use uuid::Uuid;

#[derive(Debug)]
struct Transient;
impl std::fmt::Display for Transient {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("transient_control_plane_failure")
    }
}
impl std::error::Error for Transient {}

fn private_file(path: &Path, create: bool) -> Result<File> {
    let file = OpenOptions::new()
        .read(true)
        .write(create)
        .create(create)
        .truncate(false)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW)
        .open(path)?;
    let metadata = file.metadata()?;
    // SAFETY: geteuid has no preconditions or side effects.
    let uid = unsafe { libc::geteuid() };
    ensure!(
        metadata.is_file()
            && metadata.mode() & 0o077 == 0
            && metadata.uid() == uid
            && metadata.nlink() == 1,
        "Unsafe credential or journal file"
    );
    Ok(file)
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Config {
    #[serde(default = "legacy_protocol")]
    protocol_version: u32,
    server_url: String,
    token: String,
    worker_id: Uuid,
    workspace_ref: String,
    state_dir: PathBuf,
    #[serde(default)]
    allow_insecure_localhost: bool,
}

fn legacy_protocol() -> u32 {
    1
}

impl Config {
    fn load(path: &Path) -> Result<Self> {
        let mut config: Self = serde_json::from_reader(private_file(path, false)?)?;
        ensure!(
            matches!(config.protocol_version, 1 | 2),
            "Unsupported protocol"
        );
        let url = reqwest::Url::parse(&config.server_url)?;
        let local = matches!(url.host_str(), Some("127.0.0.1" | "localhost" | "[::1]"));
        ensure!(
            url.scheme() == "https"
                || (url.scheme() == "http" && local && config.allow_insecure_localhost),
            "HTTPS required except explicitly enabled loopback development"
        );
        ensure!(
            url.username().is_empty()
                && url.password().is_none()
                && url.query().is_none()
                && url.fragment().is_none()
                && url.path() == "/",
            "Server URL must be an origin without credentials"
        );
        ensure!(config.token.len() >= 32, "Worker credential is too short");
        ensure!(
            !config.workspace_ref.is_empty(),
            "Workspace reference is required"
        );
        config.server_url = url.origin().ascii_serialization();
        Ok(config)
    }
}

struct Api {
    client: Client,
    base: String,
    token: String,
}

impl Api {
    fn new(config: &Config) -> Result<Self> {
        Ok(Self {
            client: Client::builder()
                .timeout(Duration::from_secs(15))
                .connect_timeout(Duration::from_secs(5))
                .redirect(Policy::none())
                .user_agent("piglor-loom-agent/0.2")
                .build()?,
            base: config.server_url.trim_end_matches('/').to_owned(),
            token: config.token.clone(),
        })
    }

    fn call<T: DeserializeOwned>(
        &self,
        method: Method,
        path: &str,
        body: Option<&Value>,
    ) -> Result<T> {
        let mut request = self
            .client
            .request(method, format!("{}{path}", self.base))
            .bearer_auth(&self.token);
        if let Some(body) = body {
            request = request.json(body);
        }
        let response = request.send().map_err(|_| Transient)?;
        if response.status().is_server_error() || response.status().as_u16() == 429 {
            return Err(Transient.into());
        }
        ensure!(
            response.status().is_success(),
            "Control plane rejected request (HTTP {})",
            response.status().as_u16()
        );
        let mut bytes = Vec::new();
        response
            .take(1_048_577)
            .read_to_end(&mut bytes)
            .map_err(|_| Transient)?;
        ensure!(
            bytes.len() <= 1_048_576,
            "Control plane response exceeds limit"
        );
        Ok(serde_json::from_slice(&bytes)?)
    }
}

#[derive(Deserialize)]
struct Poll {
    protocol_version: u32,
    worker_id: Uuid,
    commands: Vec<Offered>,
}

#[derive(Deserialize)]
struct Offered {
    id: Uuid,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Execution {
    protocol_version: u32,
    command_id: Uuid,
    claim_id: Uuid,
    session_id: Uuid,
    worker_id: Uuid,
    runtime: String,
    workspace_ref: String,
    phase: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    context: Option<ExecutionContext>,
}

#[derive(Deserialize, Serialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct Condition {
    source: String,
    #[serde(rename = "type")]
    event_type: String,
    resource: String,
    version: String,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct WaitContext {
    generation: u32,
    condition: Condition,
    satisfied: bool,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct ExecutionContext {
    goal_id: Uuid,
    objective: String,
    lifecycle: String,
    max_attempts: u32,
    completion_condition: Condition,
    wait: WaitContext,
    provider_session_id: Option<String>,
    // External data is deliberately separate from trusted workflow instructions.
    external_event: Option<Value>,
}

impl Execution {
    fn validate(&self, config: &Config, id: Uuid, claim: Uuid) -> Result<()> {
        ensure!(
            self.protocol_version == config.protocol_version
                && self.command_id == id
                && self.claim_id == claim,
            "Command protocol or claim mismatch"
        );
        ensure!(
            self.worker_id == config.worker_id && self.workspace_ref == config.workspace_ref,
            "Command violates local worker/workspace policy"
        );
        ensure!(
            self.runtime == "remote-demo",
            "Runtime not locally authorized"
        );
        match (self.protocol_version, &self.context) {
            (1, None) => ensure!(self.phase <= 1, "Legacy phase out of bounds"),
            (2, Some(context)) => {
                ensure!(
                    matches!(context.lifecycle.as_str(), "legacy" | "event-driven-v1")
                        && (2..=1000).contains(&context.max_attempts)
                        && self.phase < context.max_attempts
                        && context.wait.generation > 0
                        && context.provider_session_id.is_none(),
                    "Unsupported execution context"
                );
                if context.lifecycle == "legacy" {
                    ensure!(self.phase <= 1, "Legacy phase out of bounds");
                }
            }
            _ => bail!("Missing or unsupported execution context"),
        }
        Ok(())
    }

    fn outcome(&self) -> Option<&'static str> {
        let context = self.context.as_ref()?;
        if context.lifecycle != "event-driven-v1" {
            return None;
        }
        Some(if self.phase == 0 {
            "yield"
        } else if context.wait.satisfied && context.wait.condition == context.completion_condition {
            "complete"
        } else {
            // The finite runtime cannot reason about or invent a next dependency.
            "blocked"
        })
    }
}

struct Journal {
    db: Connection,
    _lock: File,
}

impl Journal {
    fn open(config: &Config) -> Result<Self> {
        fs::DirBuilder::new()
            .recursive(true)
            .mode(0o700)
            .create(&config.state_dir)?;
        let metadata = fs::symlink_metadata(&config.state_dir)?;
        // SAFETY: geteuid has no preconditions or side effects.
        let uid = unsafe { libc::geteuid() };
        ensure!(
            metadata.is_dir() && metadata.uid() == uid && metadata.mode() & 0o077 == 0,
            "State directory must be owned by this user, private, and not a symlink"
        );
        let lock = private_file(&config.state_dir.join("agent.lock"), true)?;
        lock.try_lock()
            .context("Another agent owns this state directory")?;
        let database_path = config.state_dir.join("journal.sqlite3");
        drop(private_file(&database_path, true)?);
        for suffix in ["-wal", "-shm"] {
            let path = config.state_dir.join(format!("journal.sqlite3{suffix}"));
            if fs::symlink_metadata(&path).is_ok() {
                drop(private_file(&path, false)?);
            }
        }
        let db = Connection::open(database_path)?;
        db.execute_batch("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;
            CREATE TABLE IF NOT EXISTS identity (singleton INTEGER PRIMARY KEY CHECK(singleton=1), binding TEXT NOT NULL);
            CREATE TABLE IF NOT EXISTS receipts (id TEXT PRIMARY KEY, claim_id TEXT NOT NULL,
                state TEXT NOT NULL, execution TEXT, report TEXT);")?;
        let mut identity = format!(
            "{}|{}|{}",
            config.server_url, config.worker_id, config.workspace_ref
        );
        if config.protocol_version != 1 {
            identity.push_str(&format!("|protocol:{}", config.protocol_version));
        }
        db.execute("INSERT OR IGNORE INTO identity VALUES(1,?1)", [&identity])?;
        let saved: String =
            db.query_row("SELECT binding FROM identity WHERE singleton=1", [], |r| {
                r.get(0)
            })?;
        ensure!(
            saved == identity,
            "Journal belongs to another worker/server/workspace"
        );
        Ok(Self { db, _lock: lock })
    }

    fn prepare(&self, id: Uuid) -> Result<(Uuid, String)> {
        self.db.execute(
            "INSERT OR IGNORE INTO receipts(id,claim_id,state) VALUES(?1,?2,'PREPARED')",
            params![id.to_string(), Uuid::new_v4().to_string()],
        )?;
        let (claim, state): (String, String) = self.db.query_row(
            "SELECT claim_id,state FROM receipts WHERE id=?1",
            [id.to_string()],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )?;
        Ok((Uuid::parse_str(&claim)?, state))
    }

    fn assert_recoverable(&self) -> Result<()> {
        let count: i64 = self.db.query_row(
            "SELECT count(*) FROM receipts WHERE state='RUNNING'",
            [],
            |r| r.get(0),
        )?;
        ensure!(
            count == 0,
            "Ambiguous previous execution: reconciliation required; refusing relaunch"
        );
        Ok(())
    }

    fn flush(&self, api: &Api) -> Result<()> {
        let mut statement = self
            .db
            .prepare("SELECT id,report FROM receipts WHERE state='STOPPED'")?;
        let rows =
            statement.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?;
        for row in rows {
            let (id, report) = row?;
            let _: Value = api.call(
                Method::POST,
                &format!("/v1/worker/commands/{id}/stop"),
                Some(&serde_json::from_str(&report)?),
            )?;
            self.db
                .execute("UPDATE receipts SET state='ACKNOWLEDGED' WHERE id=?1", [id])?;
        }
        Ok(())
    }

    fn execute(&self, command: &Execution) -> Result<()> {
        let changed = self.db.execute(
            "UPDATE receipts SET state='RUNNING',execution=?1 WHERE id=?2 AND state='PREPARED'",
            params![
                serde_json::to_string(command)?,
                command.command_id.to_string()
            ],
        )?;
        ensure!(changed == 1, "Execution was already admitted locally");
        let started = Instant::now();
        // This private finite subcommand cannot run user code or create children.
        let status = Command::new(std::env::current_exe()?)
            .arg("__finite")
            .env_clear()
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status();
        let success = status.is_ok_and(|s| s.success());
        let mut report = json!({"protocol_version":command.protocol_version,"claim_id":command.claim_id,
            "session_id":command.session_id,"duration_ms":started.elapsed().as_secs_f64()*1000.0,"success":success});
        if let Some(outcome) = command.outcome() {
            report["outcome"] = json!(outcome);
        }
        self.db.execute(
            "UPDATE receipts SET state='STOPPED',report=?1 WHERE id=?2",
            params![report.to_string(), command.command_id.to_string()],
        )?;
        Ok(())
    }
}

fn run(config: Config) -> Result<()> {
    let api = Api::new(&config)?;
    let journal = Journal::open(&config)?;
    journal.assert_recoverable()?;
    let mut backoff = 1;
    loop {
        let result = (|| -> Result<()> {
            journal.flush(&api)?;
            let poll: Poll = api.call(Method::GET, "/v1/worker/commands", None)?;
            ensure!(
                poll.protocol_version == 1 && poll.worker_id == config.worker_id,
                "Mailbox identity mismatch"
            );
            for offered in poll.commands {
                let (claim_id, state) = journal.prepare(offered.id)?;
                if state == "ACKNOWLEDGED" {
                    continue;
                }
                ensure!(state == "PREPARED", "Local attempt is not safe to launch");
                let command: Execution = api.call(
                    Method::POST,
                    &format!("/v1/worker/commands/{}/claim", offered.id),
                    Some(&json!({"protocol_version":config.protocol_version,"claim_id":claim_id})),
                )?;
                command.validate(&config, offered.id, claim_id)?;
                journal.execute(&command)?;
                eprintln!(
                    "{}",
                    json!({"event":"runtime_stopped","command_id":offered.id})
                );
                journal.flush(&api)?;
            }
            Ok(())
        })();
        if let Err(error) = result {
            if error.downcast_ref::<Transient>().is_none() {
                eprintln!(
                    "{}",
                    json!({"event":"agent_rejected","category":"authentication_policy_or_local_state"})
                );
                return Err(error);
            }
            // Never include exception bodies, URLs or credentials in daemon logs.
            eprintln!(
                "{}",
                json!({"event":"connection_or_delivery_retry","delay_seconds":backoff})
            );
            journal.assert_recoverable()?;
            thread::sleep(Duration::from_secs(backoff));
            backoff = (backoff * 2).min(30);
        } else {
            backoff = 1;
            thread::sleep(Duration::from_secs(2));
        }
    }
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.get(1).map(String::as_str) == Some("__finite") {
        return;
    }
    let result = (|| -> Result<()> {
        if args.len() != 3 || args[1] != "--config" {
            bail!("Usage: loom-agent --config /path/to/worker.json");
        }
        run(Config::load(Path::new(&args[2]))?)
    })();
    if result.is_err() {
        eprintln!(
            "Agent stopped: check configuration, journal ownership and unresolved execution. Credentials and error payloads are intentionally omitted."
        );
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rusqlite::OptionalExtension;

    fn config(path: &Path) -> Config {
        Config {
            protocol_version: 1,
            server_url: "http://127.0.0.1:8000".into(),
            token: "x".repeat(64),
            worker_id: Uuid::new_v4(),
            workspace_ref: "test".into(),
            state_dir: path.join("state"),
            allow_insecure_localhost: true,
        }
    }

    #[test]
    fn journal_claim_survives_restart_and_ambiguity_blocks() -> Result<()> {
        let directory = tempfile::tempdir()?;
        let config = config(directory.path());
        let id = Uuid::new_v4();
        let journal = Journal::open(&config)?;
        let first = journal.prepare(id)?;
        assert!(Journal::open(&config).is_err());
        drop(journal);
        let journal = Journal::open(&config)?;
        assert_eq!(first, journal.prepare(id)?);
        journal
            .db
            .execute("UPDATE receipts SET state='RUNNING'", [])?;
        assert!(journal.assert_recoverable().is_err());
        Ok(())
    }

    #[test]
    fn journal_cannot_move_between_workers() -> Result<()> {
        let directory = tempfile::tempdir()?;
        let mut config = config(directory.path());
        drop(Journal::open(&config)?);
        config.worker_id = Uuid::new_v4();
        assert!(Journal::open(&config).is_err());
        Ok(())
    }

    #[test]
    fn command_must_match_enrolled_policy() {
        let config = config(Path::new("unused"));
        let id = Uuid::new_v4();
        let claim = Uuid::new_v4();
        let mut command = Execution {
            context: None,
            protocol_version: 1,
            command_id: id,
            claim_id: claim,
            session_id: Uuid::new_v4(),
            worker_id: config.worker_id,
            runtime: "remote-demo".into(),
            workspace_ref: "test".into(),
            phase: 0,
        };
        assert!(command.validate(&config, id, claim).is_ok());
        command.workspace_ref = "privileged".into();
        assert!(command.validate(&config, id, claim).is_err());
    }

    #[test]
    fn repeatable_context_is_explicit_and_finite_outcome_is_bounded() -> Result<()> {
        let mut config = config(Path::new("unused"));
        config.protocol_version = 2;
        let id = Uuid::new_v4();
        let claim = Uuid::new_v4();
        let condition = json!({"source":"gitlab","type":"pipeline.completed",
            "resource":"opaque","version":"2"});
        let mut command: Execution = serde_json::from_value(json!({
            "protocol_version":2,"command_id":id,"claim_id":claim,
            "session_id":Uuid::new_v4(),"worker_id":config.worker_id,
            "runtime":"remote-demo","workspace_ref":"test","phase":0,
            "context":{"goal_id":Uuid::new_v4(),"objective":"Wait",
                "lifecycle":"event-driven-v1","max_attempts":3,
                "completion_condition":condition,
                "wait":{"generation":1,"condition":condition,"satisfied":false},
                "provider_session_id":null,"external_event":null}
        }))?;
        command.validate(&config, id, claim)?;
        assert_eq!(command.outcome(), Some("yield"));
        command.phase = 1;
        assert_eq!(command.outcome(), Some("blocked"));
        command.context.as_mut().unwrap().wait.satisfied = true;
        assert_eq!(command.outcome(), Some("complete"));
        command.context.as_mut().unwrap().wait.condition.version = "stale".into();
        assert_eq!(command.outcome(), Some("blocked"));
        command.context.as_mut().unwrap().provider_session_id = Some("provider".into());
        assert!(command.validate(&config, id, claim).is_err());
        command.context.as_mut().unwrap().provider_session_id = None;
        command.phase = 3;
        assert!(command.validate(&config, id, claim).is_err());
        command.phase = 1;
        command.context.as_mut().unwrap().lifecycle = "legacy".into();
        assert_eq!(command.outcome(), None);
        command.validate(&config, id, claim)?;
        command.context = None;
        assert!(command.validate(&config, id, claim).is_err());
        command.protocol_version = 1;
        assert!(command.validate(&config, id, claim).is_err());
        Ok(())
    }

    #[test]
    fn journal_cannot_silently_switch_protocol() -> Result<()> {
        let directory = tempfile::tempdir()?;
        let mut config = config(directory.path());
        drop(Journal::open(&config)?);
        config.protocol_version = 2;
        assert!(Journal::open(&config).is_err());
        Ok(())
    }

    #[test]
    fn journal_has_no_plaintext_credential() -> Result<()> {
        let directory = tempfile::tempdir()?;
        let config = config(directory.path());
        let journal = Journal::open(&config)?;
        let value: Option<String> = journal
            .db
            .query_row("SELECT binding FROM identity", [], |r| r.get(0))
            .optional()?;
        assert!(!value.unwrap().contains(&config.token));
        Ok(())
    }

    #[test]
    fn journal_rejects_symlink_children() -> Result<()> {
        let directory = tempfile::tempdir()?;
        let config = config(directory.path());
        drop(Journal::open(&config)?);
        let target = directory.path().join("elsewhere");
        fs::write(&target, b"untouched")?;
        fs::remove_file(config.state_dir.join("agent.lock"))?;
        std::os::unix::fs::symlink(&target, config.state_dir.join("agent.lock"))?;
        assert!(Journal::open(&config).is_err());
        assert_eq!(fs::read(target)?, b"untouched");
        Ok(())
    }

    #[test]
    fn configuration_transport_permissions_and_origin() -> Result<()> {
        use std::os::unix::fs::PermissionsExt;
        let directory = tempfile::tempdir()?;
        let mut config = config(directory.path());
        let path = directory.path().join("worker.json");
        fs::write(&path, serde_json::to_vec(&config)?)?;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o644))?;
        assert!(Config::load(&path).is_err());
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600))?;
        assert!(Config::load(&path).is_ok());
        for url in [
            "http://example.org",
            "https://user:password@example.org",
            "https://example.org/path",
            "https://example.org?token=x",
        ] {
            config.server_url = url.into();
            fs::write(&path, serde_json::to_vec(&config)?)?;
            assert!(Config::load(&path).is_err());
        }
        config.server_url = "https://EXAMPLE.org/".into();
        fs::write(&path, serde_json::to_vec(&config)?)?;
        assert_eq!(Config::load(&path)?.server_url, "https://example.org");
        Ok(())
    }

    #[test]
    fn redirects_are_permanent_failures() -> Result<()> {
        use std::io::Write;
        let listener = std::net::TcpListener::bind("127.0.0.1:0")?;
        let mut config = config(Path::new("unused"));
        config.server_url = format!("http://{}", listener.local_addr()?);
        let server = thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut request = [0; 4096];
            assert!(stream.read(&mut request).unwrap() > 0);
            stream.write_all(b"HTTP/1.1 302 Found\r\nLocation: https://example.org\r\nContent-Length: 0\r\nConnection: close\r\n\r\n").unwrap();
        });
        let error = Api::new(&config)?
            .call::<Value>(Method::GET, "/", None)
            .unwrap_err();
        assert!(error.downcast_ref::<Transient>().is_none());
        server.join().unwrap();
        Ok(())
    }
}
