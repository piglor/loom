"""Render a secret-free, inline-build Compose snapshot for Coolify.

Run with Python 3.12 or newer; no third-party dependencies are required.
Output is a generated deployment artifact, not a place to store credentials.
"""

import base64
import gzip
import hashlib
import io
import tarfile
from pathlib import Path


def render(root: Path | None = None) -> tuple[str, str]:
    root = (root or Path(__file__).resolve().parents[2]).resolve()
    files = [root / "pyproject.toml", root / "requirements.lock"]
    files += sorted((root / "server" / "loom").glob("*.py"))
    files += sorted((root / "server" / "loom").glob("*.sql"))
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode="w") as bundle:
        for path in files:
            if path.is_symlink() or path.resolve() != path or not path.is_file():
                raise ValueError(f"Not a regular source file: {path}")
            data = path.read_bytes()
            info = tarfile.TarInfo(str(path.relative_to(root)))
            info.size = len(data)
            info.mode = 0o644
            bundle.addfile(info, io.BytesIO(data))
    payload = archive.getvalue()
    encoded = base64.b64encode(gzip.compress(payload, mtime=0)).decode()
    recipe = root / "Dockerfile"
    if recipe.is_symlink() or not recipe.is_file():
        raise ValueError("Not a regular Dockerfile")
    dockerfile = recipe.read_text()
    digest = hashlib.sha256(
        payload + b"\0Dockerfile\0" + dockerfile.encode()
    ).hexdigest()
    copies = "COPY pyproject.toml requirements.lock ./\nCOPY server ./server"
    if copies not in dockerfile:
        raise ValueError("Dockerfile source-copy contract changed; review renderer")
    extraction = (
        'RUN python -c "import base64,io,tarfile; '
        f"tarfile.open(fileobj=io.BytesIO(base64.b64decode('{encoded}')))"
        ".extractall(path='/app',filter='data')\""
    )
    dockerfile = dockerfile.replace(copies, extraction)
    compose = (root / "deploy/coolify/compose.yaml").read_text()
    original = (
        "  build:\n    context: ../..\n    dockerfile: Dockerfile\n"
        "  image: piglor-loom:local\n"
    )
    if compose.count(original) != 1:
        raise ValueError("Compose build contract changed; review renderer")
    inline = "\n".join("      " + line for line in dockerfile.splitlines())
    replacement = (
        "  build:\n    context: .\n    dockerfile_inline: |\n"
        + inline
        + f"\n  image: piglor-loom:snapshot-{digest[:16]}\n"
        + "  pull_policy: build\n"
    )
    return compose.replace(original, replacement), digest


if __name__ == "__main__":
    print(render()[0], end="")
