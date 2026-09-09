//! Version-tested Codex stdio adapter. No terminal automation or guessed session.
use anyhow::{Context, Result, bail, ensure};
use serde_json::{Value, json};
use std::{
    collections::VecDeque,
    io::{BufRead, BufReader, Write},
    os::fd::AsRawFd,
    os::unix::process::CommandExt,
    path::Path,
    process::{Child, ChildStdin, Command, Stdio},
    sync::mpsc::{self, Receiver},
    thread,
    time::{Duration, Instant},
};

pub struct Codex {
    child: Child,
    input: ChildStdin,
    messages: Receiver<Result<Value, ()>>,
    sequence: u64,
    deadline: Instant,
    stopped: bool,
    pending: VecDeque<Value>,
    thread_id: Option<String>,
    binding_confirmed: bool,
    turn_admitted: bool,
}

pub struct TurnResult {
    pub text: String,
    pub usage: Option<Value>,
}

impl Codex {
    pub fn start(binary: &Path, workspace: &Path, timeout: Duration) -> Result<Self> {
        ensure!(
            workspace.is_absolute() && workspace.is_dir(),
            "Absolute workspace required"
        );
        let mut child = Command::new(binary)
            .args(["app-server", "--stdio"])
            .current_dir(workspace)
            .process_group(0)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()?;
        let input = child.stdin.take().context("Missing private stdin")?;
        let output = child.stdout.take().context("Missing private stdout")?;
        let (sender, messages) = mpsc::sync_channel(64);
        thread::spawn(move || {
            let mut reader = BufReader::new(output);
            loop {
                // Bound each protocol frame before allocating arbitrary provider output.
                let mut bytes = Vec::new();
                let read =
                    std::io::Read::take(&mut reader, 1_048_577).read_until(b'\n', &mut bytes);
                if read.is_err() || bytes.len() > 1_048_576 {
                    let _ = sender.send(Err(()));
                    break;
                }
                if bytes.is_empty() {
                    break;
                }
                let value = serde_json::from_slice(&bytes).map_err(|_| ());
                if sender.send(value).is_err() {
                    break;
                }
            }
        });
        let mut adapter = Self {
            child,
            input,
            messages,
            sequence: 0,
            deadline: Instant::now() + timeout,
            stopped: false,
            pending: VecDeque::new(),
            thread_id: None,
            binding_confirmed: false,
            turn_admitted: false,
        };
        // SAFETY: the descriptor is an owned live ChildStdin. Set nonblocking
        // so a stalled provider cannot defeat the runtime deadline on writes.
        let flags = unsafe { libc::fcntl(adapter.input.as_raw_fd(), libc::F_GETFL) };
        ensure!(flags >= 0, "Cannot inspect runtime stdin flags");
        let result = unsafe {
            libc::fcntl(
                adapter.input.as_raw_fd(),
                libc::F_SETFL,
                flags | libc::O_NONBLOCK,
            )
        };
        ensure!(result == 0, "Cannot bound runtime stdin writes");
        adapter.rpc(
            "initialize",
            json!({"clientInfo":{"name":"piglor_loom","version":"0.2.0"},
            "capabilities":{"experimentalApi":false}}),
        )?;
        adapter.send(json!({"method":"initialized"}))?;
        Ok(adapter)
    }

    fn send(&mut self, value: Value) -> Result<()> {
        let mut bytes = serde_json::to_vec(&value)?;
        ensure!(bytes.len() <= 65536, "Runtime request exceeds limit");
        bytes.push(b'\n');
        let mut pending = bytes.as_slice();
        while !pending.is_empty() {
            ensure!(
                Instant::now() < self.deadline,
                "Runtime write deadline reached"
            );
            match self.input.write(pending) {
                Ok(0) => bail!("Runtime stdin closed"),
                Ok(count) => pending = &pending[count..],
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                    thread::sleep(Duration::from_millis(5))
                }
                Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
                Err(_) => bail!("Runtime stdin unavailable"),
            }
        }
        Ok(())
    }

    fn read_message(&self) -> Result<Value> {
        let remaining = self
            .deadline
            .checked_duration_since(Instant::now())
            .context("Runtime deadline reached")?;
        let message = self
            .messages
            .recv_timeout(remaining.min(Duration::from_secs(120)))
            .map_err(|_| anyhow::anyhow!("Runtime disconnected or timed out"))?
            .map_err(|_| anyhow::anyhow!("Invalid or oversized runtime frame"))?;
        // Approval/tool requests cannot keep inference alive indefinitely. Caller
        // gets a typed boundary failure and Drop terminates the owned process group.
        if message.get("method").is_some() && message.get("id").is_some() {
            bail!("Runtime requested unsupported human/tool interaction");
        }
        Ok(message)
    }

    fn next(&mut self) -> Result<Value> {
        match self.pending.pop_front() {
            Some(message) => Ok(message),
            None => self.read_message(),
        }
    }

    fn rpc(&mut self, method: &str, params: Value) -> Result<Value> {
        self.sequence += 1;
        let id = self.sequence;
        self.send(json!({"id":id,"method":method,"params":params}))?;
        loop {
            let message = self.read_message()?;
            if message.get("id").and_then(Value::as_u64) == Some(id) {
                ensure!(
                    message.get("error").is_none(),
                    "Runtime rejected protocol request"
                );
                return message.get("result").cloned().context("Missing RPC result");
            }
            ensure!(
                self.pending.len() < 128,
                "Too many notifications before RPC acknowledgment"
            );
            self.pending.push_back(message);
        }
    }

    /// Opening a provider context never starts a model turn.
    fn open_thread(&mut self, workspace: &Path, thread_id: Option<&str>) -> Result<String> {
        ensure!(
            self.thread_id.is_none() && !self.stopped,
            "Runtime context already opened or stopped"
        );
        let mut params = json!({"cwd":workspace,"sandbox":"read-only","approvalPolicy":"never"});
        let method = if let Some(id) = thread_id {
            params["threadId"] = json!(id);
            "thread/resume"
        } else {
            "thread/start"
        };
        let result = self.rpc(method, params)?;
        let id = result["thread"]["id"]
            .as_str()
            .context("Missing provider thread ID")?;
        if let Some(expected) = thread_id {
            ensure!(id == expected, "Provider resumed a different thread");
        }
        ensure!(
            !id.is_empty()
                && id.chars().count() <= 256
                && !id.chars().any(|c| c.is_whitespace() || c.is_control()),
            "Invalid provider thread ID"
        );
        self.thread_id = Some(id.to_owned());
        Ok(id.to_owned())
    }

    /// The callback must durably persist this exact thread and validate any
    /// remote acknowledgment before returning Ok. Failure leaves inference
    /// disabled for this process; it must be stopped, never blindly retried.
    pub fn open_bound_thread(
        &mut self,
        workspace: &Path,
        thread_id: Option<&str>,
        persist: impl FnOnce(&str) -> Result<()>,
    ) -> Result<String> {
        let id = self.open_thread(workspace, thread_id)?;
        persist(&id)?;
        self.confirm_persisted_binding(&id)?;
        Ok(id)
    }

    fn confirm_persisted_binding(&mut self, thread_id: &str) -> Result<()> {
        ensure!(
            self.thread_id.as_deref() == Some(thread_id) && !self.stopped && !self.turn_admitted,
            "Persisted binding does not match this runtime context"
        );
        self.binding_confirmed = true;
        Ok(())
    }

    pub fn turn(
        &mut self,
        thread_id: &str,
        input: &str,
        output_schema: Value,
    ) -> Result<TurnResult> {
        ensure!(
            self.binding_confirmed && self.thread_id.as_deref() == Some(thread_id),
            "Durable provider binding must be confirmed before inference"
        );
        ensure!(
            !self.turn_admitted && !self.stopped,
            "Attempt already admitted or runtime stopped"
        );
        // Mark before writing: an ambiguous transport failure must not allow a
        // second turn/start, even if the provider never acknowledged the first.
        self.turn_admitted = true;
        let result = self.rpc(
            "turn/start",
            json!({"threadId":thread_id,
            "input":[{"type":"text","text":input}],"outputSchema":output_schema}),
        )?;
        let turn_id = result["turn"]["id"]
            .as_str()
            .context("Missing provider turn ID")?;
        let mut text = String::new();
        let mut usage = None;
        loop {
            let message = self.next()?;
            let params = &message["params"];
            if params["threadId"] != thread_id {
                continue;
            }
            match message["method"].as_str() {
                Some("thread/tokenUsage/updated") => {
                    usage = Some(params["tokenUsage"].clone());
                }
                Some("item/completed")
                    if params["turnId"] == turn_id && params["item"]["type"] == "agentMessage" =>
                {
                    text = params["item"]["text"].as_str().unwrap_or("").to_owned();
                }
                Some("turn/completed") if params["turn"]["id"] == turn_id => {
                    ensure!(
                        params["turn"]["status"] == "completed",
                        "Runtime turn did not complete successfully"
                    );
                    ensure!(!text.is_empty(), "Runtime supplied no final output");
                    return Ok(TurnResult { text, usage });
                }
                _ => {}
            }
        }
    }

    /// Stop and reap the app-server process group. Deployment supervision must
    /// additionally contain children that could escape a Unix process group.
    pub fn stop(&mut self) -> Result<()> {
        if !self.stopped {
            let pid = i32::try_from(self.child.id())?;
            // SAFETY: this child was created in its own group and has not been
            // reaped, so its process-group identifier cannot have been recycled.
            let result = unsafe { libc::kill(-pid, libc::SIGKILL) };
            ensure!(
                result == 0 || std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH),
                "Could not stop runtime process group"
            );
            self.child.wait()?;
            self.stopped = true;
        }
        Ok(())
    }
}

impl Drop for Codex {
    fn drop(&mut self) {
        let _ = self.stop();
    }
}
