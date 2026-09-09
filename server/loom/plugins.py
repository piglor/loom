"""Trusted built-in integration composition, not a dynamic plugin loader."""

import logging
import os
from typing import Protocol

from fastapi import APIRouter

from loom.github_plugin import GitHubPlugin
from loom.gitlab import GitLabPlugin


class IntegrationPlugin(Protocol):
    name: str

    def routes(self, authenticate) -> APIRouter: ...

    def reconcile_pending(self) -> None: ...


def configured_plugins(store) -> tuple[IntegrationPlugin, ...]:
    registry = {"github": GitHubPlugin, "gitlab": GitLabPlugin}
    selected = [
        name.strip()
        for name in os.environ.get("LOOM_INTEGRATIONS", "github,gitlab").split(",")
        if name.strip()
    ]
    if len(selected) != len(set(selected)) or set(selected) - registry.keys():
        raise ValueError("LOOM_INTEGRATIONS contains duplicate or unsupported plugins")
    return tuple(registry[name](store) for name in selected)


def reconcile_integrations(store):
    for plugin in configured_plugins(store):
        try:
            plugin.reconcile_pending()
        except Exception as error:
            # One integration outage must not stop unrelated event delivery.
            logging.getLogger("loom.integrations").error(
                "integration_reconcile_failed source=%s error_type=%s",
                plugin.name,
                type(error).__name__,
            )
