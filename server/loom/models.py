from typing import Annotated, Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, model_validator


class Model(BaseModel):
    model_config = ConfigDict(extra="forbid")


class Condition(Model):
    source: str = Field(min_length=1, max_length=80)
    type: str = Field(min_length=1, max_length=120)
    resource: str = Field(min_length=1, max_length=256)
    version: str = Field(min_length=1, max_length=128)


class GoalCreate(Model):
    title: str = Field(min_length=1, max_length=200)
    objective: str = Field(min_length=1, max_length=8000)
    condition: Condition
    runtime: Literal["demo", "remote-demo"] = "demo"
    worker_id: str | None = Field(default=None, max_length=128)
    completion_condition: Condition | None = None
    max_attempts: int = Field(default=100, ge=2, le=1000, strict=True)

    @model_validator(mode="after")
    def binding_required(self):
        if (self.runtime != "demo") != (self.worker_id is not None):
            raise ValueError("Remote runtime requires an explicit worker binding")
        return self


class EventCreate(Condition):
    delivery_id: str = Field(min_length=1, max_length=128)
    goal_id: UUID
    generation: int = Field(ge=1)


class WorkflowInput(Model):
    goal_id: UUID


class WorkerEnroll(Model):
    workspace_ref: str = Field(pattern=r"^[a-zA-Z0-9_-]{1,80}$")
    labels: dict[str, str] = Field(default_factory=dict, max_length=20)
    protocol_version: Literal[1, 2] = 1


class ClaimRequest(Model):
    protocol_version: Literal[1, 2] = 1
    claim_id: UUID


ProviderSessionId = Annotated[
    str, Field(min_length=1, max_length=256, pattern=r"^[^\s\p{Cc}]+$")
]


class StopReport(ClaimRequest):
    session_id: UUID
    duration_ms: float = Field(ge=0, le=86400000, allow_inf_nan=False)
    success: bool
    provider_session_id: ProviderSessionId | None = None
    outcome: Literal["yield", "complete", "blocked"] | None = None

    @model_validator(mode="after")
    def versioned_outcome(self):
        if self.protocol_version == 1 and self.outcome is not None:
            raise ValueError("Explicit outcomes require protocol 2")
        return self


class SessionBinding(ClaimRequest):
    session_id: UUID
    provider_session_id: ProviderSessionId


class PrepareWait(ClaimRequest):
    protocol_version: Literal[2] = 2
    session_id: UUID
    expected_generation: int = Field(ge=1, strict=True)
    condition: Condition
