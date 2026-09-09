import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    database_url: str
    api_token: str
    organization: str = "local"

    @classmethod
    def from_env(cls):
        token = os.environ.get("LOOM_API_TOKEN", "")
        if len(token) < 32:
            raise ValueError("LOOM_API_TOKEN must contain at least 32 characters")
        url = os.environ.get("LOOM_DATABASE_URL", "")
        if not url:
            raise ValueError("LOOM_DATABASE_URL is required")
        return cls(url, token, os.environ.get("LOOM_ORGANIZATION", "local"))
