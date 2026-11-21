"""Try the portable instruction in Pi without installing any smem-specific adapter."""

import argparse
import json
import os
import shutil
import subprocess
import tempfile
import tomllib
from pathlib import Path

from clients import environment, routing
from handoff import READER, calls, field_correct, scratch


def run(binaries: Path, cases: dict[str, str], model: str) -> dict[str, object]:
    node, shim = (
        shutil.which("node"),
        shutil.which("pi.cmd" if os.name == "nt" else "pi"),
    )
    if not shim:
        raise RuntimeError("Pi CLI unavailable")
    command = [shim]
    if os.name == "nt":
        if not node:
            raise RuntimeError("Pi needs node")
        command = [
            node,
            str(
                Path(shim).parent
                / "node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js"
            ),
        ]
    with tempfile.TemporaryDirectory(
        prefix="smem-pi-", dir=scratch(), ignore_cleanup_errors=True
    ) as temporary:
        root = Path(temporary)
        repo = root / "project"
        repo.mkdir()
        env = environment(root / "user", binaries)
        subprocess.run(
            ["git", "init", "-q", "--template="], cwd=repo, env=env, check=True
        )
        binary = next(binaries.glob("payload*"))
        init = subprocess.check_output(
            [str(binary), "init", "--agents", "codex"],
            cwd=repo,
            env=env,
            encoding="utf-8",
        )
        rule = next(
            line
            for line in (repo / "AGENTS.md").read_text(encoding="utf-8").splitlines()
            if "mem search" in line and "mem add" in line
        )
        (repo / "AGENTS.md").unlink()
        (repo / ".smem-observations").mkdir()
        old, new = cases["claude-2"], cases["codex-3"]
        subprocess.run(
            [str(binary), "add", f"项目发版放行人为{old}。"],
            cwd=repo,
            env=env,
            check=True,
            capture_output=True,
        )
        routing("codex", env, repo, model, False)
        provider = tomllib.loads(
            (Path(env["CODEX_HOME"]) / "config.toml").read_text(encoding="utf-8")
        )["model_providers"]["bench"]
        profile = root / "pi"
        profile.mkdir()
        env["PI_CODING_AGENT_DIR"] = str(profile)
        config = {
            "providers": {
                "bench": {
                    "baseUrl": provider["base_url"],
                    "api": "openai-responses",
                    "apiKey": "$SMEM_BENCH_ROUTE_KEY",
                    "headers": {
                        header: "$" + key
                        for header, key in provider["env_http_headers"].items()
                    },
                    "models": [{"id": model, "input": ["text"]}],
                }
            }
        }
        (profile / "models.json").write_text(json.dumps(config), encoding="utf-8")
        if os.name == "nt":
            (profile / "settings.json").write_text(
                json.dumps(
                    {
                        "shellPath": str(
                            Path(os.environ["ProgramFiles"]) / "Git/bin/bash.exe"
                        )
                    }
                ),
                encoding="utf-8",
            )

        def prompt(text: str) -> tuple[int, str]:
            args = [
                *command,
                "--provider",
                "bench",
                "--model",
                model,
                "--no-session",
                "--no-extensions",
                "--no-skills",
                "--no-context-files",
                "--no-prompt-templates",
                "--no-themes",
                "--tools",
                "read,bash",
                "--append-system-prompt",
                rule,
                "-p",
                text,
            ]
            with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
                outcome = subprocess.run(
                    args,
                    cwd=repo,
                    env=env,
                    stdout=stdout,
                    stderr=stderr,
                    timeout=120,
                    check=False,
                )
                stdout.seek(0)
                return outcome.returncode, stdout.read().decode(
                    "utf-8", errors="replace"
                )

        read_code, answer = prompt(READER)
        read_events = calls(repo, old)
        write_code, _ = prompt(
            f"本项目以后的交接单统一使用流转代号{new}。请落实这项长期约定。"
        )
        note_saved = any(
            new in path.read_text(encoding="utf-8")
            for path in (repo / ".mem/memory").glob("*.md")
        )
        return {
            "client": "Pi",
            "model": model,
            "init_exposed_generic_rule": rule in init,
            "read_exit": read_code,
            "read_call": any(
                event["body_hit"] and event["ok"] for event in read_events
            ),
            "used": field_correct(answer, old),
            "write_exit": write_code,
            "write_via_mem": note_saved
            and any(
                event["verb"] == "add" and event["ok"] for event in calls(repo, new)
            ),
        }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--bin", required=True, type=Path)
    parser.add_argument("--cases", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--model", required=True)
    args = parser.parse_args()
    if args.out.exists():
        raise SystemExit("Choose a new report path")
    result = run(
        args.bin.resolve(),
        json.loads(args.cases.read_text(encoding="utf-8")),
        args.model,
    )
    args.out.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    print(result)


if __name__ == "__main__":
    main()
