//! Per-attempt containment, separate from provider protocol and delivery.
use crate::codex::Codex;
use anyhow::{Context, Result, ensure};
use serde::{Deserialize, Serialize};
use std::{
    io::Read,
    path::{Path, PathBuf},
    process::{Command, Stdio},
    thread,
    time::{Duration, Instant},
};
use uuid::Uuid;

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ContainerConfig {
    pub docker_binary: PathBuf,
    pub image: String,
    pub workspace: PathBuf,
    pub session_home: PathBuf,
}

impl ContainerConfig {
    pub fn validate(&self) -> Result<()> {
        ensure!(
            self.docker_binary.is_absolute() && self.docker_binary.is_file(),
            "Absolute Docker binary required"
        );
        ensure!(
            self.image.len() == 71
                && self.image.starts_with("sha256:")
                && self.image[7..].bytes().all(|b| b.is_ascii_hexdigit()),
            "Pin the runtime image by local sha256 image ID"
        );
        for path in [&self.workspace, &self.session_home] {
            ensure!(
                path.is_absolute()
                    && path.is_dir()
                    && path.canonicalize()? == *path
                    && !path.to_string_lossy().contains(',')
                    && path.components().count() >= 4,
                "Dedicated canonical runtime directories required"
            );
        }
        ensure!(
            !self.workspace.starts_with(&self.session_home)
                && !self.session_home.starts_with(&self.workspace),
            "Runtime mounts must be separate"
        );
        Ok(())
    }

    /// Ensure runtime-visible mount roots cannot contain worker credentials or
    /// the durable receipt journal, and cannot be nested inside those paths.
    pub fn validate_isolated(&self, private_paths: &[PathBuf]) -> Result<()> {
        self.validate()?;
        for private in private_paths {
            ensure!(
                private.is_absolute(),
                "Private worker paths must be absolute"
            );
            for mount in [&self.workspace, &self.session_home] {
                ensure!(
                    !private.starts_with(mount) && !mount.starts_with(private),
                    "Runtime mounts overlap worker credentials or journal"
                );
            }
        }
        Ok(())
    }
}

pub struct ContainerAttempt {
    docker: PathBuf,
    name: String,
    runtime: Option<Codex>,
    removed: bool,
}

fn control(binary: &Path, arguments: &[&str]) -> Result<String> {
    let mut child = docker_command(binary)
        .args(arguments)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()?;
    let deadline = Instant::now() + Duration::from_secs(30);
    let status = loop {
        if let Some(status) = child.try_wait()? {
            break status;
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            anyhow::bail!("Container control deadline reached");
        }
        thread::sleep(Duration::from_millis(20));
    };
    ensure!(
        status.success(),
        "Container control failed; execution remains unresolved"
    );
    let mut output = String::new();
    child
        .stdout
        .take()
        .context("Missing container control output")?
        .take(4097)
        .read_to_string(&mut output)?;
    ensure!(
        output.len() <= 4096,
        "Container control output exceeds limit"
    );
    Ok(output)
}

fn docker_command(binary: &Path) -> Command {
    let mut command = Command::new(binary);
    // Bind mounts refer to this worker's filesystem, never an inherited remote
    // Docker context. Rootless/remote engines need a separately tested policy.
    command.args(["--host", "unix:///var/run/docker.sock"]);
    command
        .env_remove("DOCKER_CONTEXT")
        .env_remove("DOCKER_HOST");
    command
}

impl ContainerAttempt {
    pub fn start(config: &ContainerConfig, command_id: Uuid) -> Result<Self> {
        config.validate()?;
        let name = format!("loom-attempt-{command_id}");
        let mut attempt = Self {
            docker: config.docker_binary.clone(),
            name,
            runtime: None,
            removed: false,
        };
        // Never reuse an existing name. An ambiguous prior create/start must be
        // reconciled by the operator, not removed by a subsequent launch.
        if control(
            &attempt.docker,
            &[
                "create",
                "--name",
                &attempt.name,
                "--label",
                &format!("piglor.loom.command={command_id}"),
                "--interactive",
                "--read-only",
                "--cap-drop=ALL",
                "--security-opt=no-new-privileges",
                "--pids-limit=128",
                "--memory=1g",
                "--cpus=1",
                "--user=1000:1000",
                "--tmpfs",
                "/tmp:rw,nosuid,nodev,size=64m",
                "--mount",
                &format!(
                    "type=bind,src={},dst=/workspace,readonly",
                    config.workspace.display()
                ),
                "--mount",
                &format!(
                    "type=bind,src={},dst=/session",
                    config.session_home.display()
                ),
                "--workdir=/workspace",
                "--env=CODEX_HOME=/session",
                "--env=HOME=/tmp",
                "--entrypoint=/usr/bin/timeout",
                &config.image,
                "--signal=KILL",
                "300",
                "codex",
                "app-server",
                "--stdio",
            ],
        )
        .is_err()
        {
            // Creation may have happened remotely but acknowledgment was lost.
            // Do not remove an object whose ownership was not confirmed here.
            attempt.removed = true;
            anyhow::bail!("Container creation failed or is ambiguous");
        }
        let mut command = docker_command(&attempt.docker);
        command.args(["start", "--attach", "--interactive", &attempt.name]);
        attempt.runtime = Some(Codex::from_command(command, Duration::from_secs(290))?);
        Ok(attempt)
    }

    pub fn runtime(&mut self) -> Result<&mut Codex> {
        self.runtime
            .as_mut()
            .context("Container runtime unavailable")
    }

    pub fn stop(&mut self) -> Result<()> {
        if !self.removed {
            control(&self.docker, &["rm", "--force", &self.name])?;
            self.removed = true;
        }
        if let Some(runtime) = &mut self.runtime {
            runtime.stop()?;
        }
        Ok(())
    }
}

impl Drop for ContainerAttempt {
    fn drop(&mut self) {
        let _ = self.stop();
    }
}
