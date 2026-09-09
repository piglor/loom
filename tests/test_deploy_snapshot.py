import base64
import importlib.util
import io
import re
import shutil
import tarfile
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "render_snapshot", ROOT / "deploy/coolify/render_snapshot.py"
)
renderer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(renderer)


def test_snapshot_is_deterministic_and_allowlisted():
    compose, digest = renderer.render()
    assert renderer.render() == (compose, digest)
    assert len(compose) < 100_000
    encoded = re.search(r"base64.b64decode\('([^']+)'\)", compose)[1]
    with tarfile.open(fileobj=io.BytesIO(base64.b64decode(encoded))) as bundle:
        names = bundle.getnames()
        assert "pyproject.toml" in names
        assert "server/loom/schema.sql" in names
        for name in names:
            assert name in {"pyproject.toml", "requirements.lock"} or (
                Path(name).parent == Path("server/loom")
                and Path(name).suffix in {".py", ".sql"}
            )
            assert bundle.extractfile(name).read() == (ROOT / name).read_bytes()
    assert "${LOOM_API_TOKEN:?required}" in compose
    assert "dockerfile_inline:" in compose


def test_snapshot_rejects_source_symlinks(tmp_path):
    (tmp_path / "pyproject.toml").symlink_to(ROOT / "pyproject.toml")
    shutil.copyfile(ROOT / "requirements.lock", tmp_path / "requirements.lock")
    with pytest.raises(ValueError, match="Not a regular source file"):
        renderer.render(tmp_path)


def test_snapshot_rejects_source_directory_symlinks(tmp_path):
    shutil.copyfile(ROOT / "pyproject.toml", tmp_path / "pyproject.toml")
    shutil.copyfile(ROOT / "requirements.lock", tmp_path / "requirements.lock")
    (tmp_path / "server").symlink_to(ROOT / "server", target_is_directory=True)
    with pytest.raises(ValueError, match="Not a regular source file"):
        renderer.render(tmp_path)


def test_snapshot_identity_includes_build_recipe(tmp_path):
    for name in ["pyproject.toml", "requirements.lock", "Dockerfile"]:
        shutil.copyfile(ROOT / name, tmp_path / name)
    shutil.copytree(ROOT / "deploy/coolify", tmp_path / "deploy/coolify")
    _, before = renderer.render(tmp_path)
    recipe = tmp_path / "Dockerfile"
    recipe.write_text(recipe.read_text() + "\nLABEL test=changed\n")
    _, after = renderer.render(tmp_path)
    assert before != after
