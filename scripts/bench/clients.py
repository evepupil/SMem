"""Isolated local CLI clients for an opt-in synthetic benchmark (Python 3.11+)."""

import json
import os
import shutil
import subprocess
import tempfile
import tomllib
from pathlib import Path
from typing import Any


def executable(client: str) -> list[str]:
    if os.name == "nt":
        shim = shutil.which(client + ".cmd")
        if shim:
            modules = Path(shim).parent / "node_modules"
            if client == "claude":
                native = modules / "@anthropic-ai/claude-code/bin/claude.exe"
                if native.is_file():
                    return [str(native)]
            if client == "codex":
                native_bins = list(
                    (modules / "@openai/codex").glob(
                        "node_modules/@openai/codex-win*/vendor/*/bin/codex.exe"
                    )
                )
                if native_bins:
                    return [str(native_bins[0])]
    binary = shutil.which(client)
    if not binary:
        raise RuntimeError(f"Missing {client} CLI")
    return [binary]


def environment(home: Path, binaries: Path) -> dict[str, str]:
    keep = {
        "PATH",
        "PATHEXT",
        "SYSTEMROOT",
        "SYSTEMDRIVE",
        "WINDIR",
        "TEMP",
        "TMP",
        "TMPDIR",
        "COMSPEC",
        "PROGRAMFILES",
        "PROGRAMFILES(X86)",
        "PROGRAMW6432",
        "PROGRAMDATA",
        "PUBLIC",
        "COMPUTERNAME",
        "USERNAME",
        "NUMBER_OF_PROCESSORS",
        "LANG",
        "LC_ALL",
        "HTTPS_PROXY",
        "HTTP_PROXY",
        "NO_PROXY",
    }
    env = {key: value for key, value in os.environ.items() if key.upper() in keep}
    for dirname in [".claude", ".codex", "appdata", "local", ".config"]:
        (home / dirname).mkdir(parents=True, exist_ok=True)
    env.update(
        HOME=str(home),
        USERPROFILE=str(home),
        APPDATA=str(home / "appdata"),
        LOCALAPPDATA=str(home / "local"),
        XDG_CONFIG_HOME=str(home / ".config"),
        CODEX_HOME=str(home / ".codex"),
        CLAUDE_CONFIG_DIR=str(home / ".claude"),
        GIT_CONFIG_NOSYSTEM="1",
        GIT_CONFIG_GLOBAL=str(home / ".gitconfig"),
        CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
        DISABLE_AUTOUPDATER="1",
        DISABLE_TELEMETRY="1",
        CLAUDE_CODE_DISABLE_AUTO_MEMORY="1",
        NO_COLOR="1",
        PATH=str(binaries) + os.pathsep + os.environ["PATH"],
    )
    return env


def routing(
    client: str, env: dict[str, str], repo: Path, model: str, native: bool
) -> None:
    if client == "claude":
        settings = (
            Path(os.environ.get("CLAUDE_CONFIG_DIR", str(Path.home() / ".claude")))
            / "settings.json"
        )
        auth: dict[str, str] = (
            json.loads(settings.read_text(encoding="utf-8-sig")).get("env", {})
            if settings.exists()
            else {}
        )
        for key in [
            "ANTHROPIC_BASE_URL",
            "ANTHROPIC_AUTH_TOKEN",
            "ANTHROPIC_API_KEY",
            "NODE_EXTRA_CA_CERTS",
        ]:
            if value := os.environ.get(key) or auth.get(key):
                env[key] = value
        env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "0" if native else "1"
        env["ANTHROPIC_SMALL_FAST_MODEL"] = model
        for tier in ["SONNET", "HAIKU", "OPUS"]:
            env[f"ANTHROPIC_DEFAULT_{tier}_MODEL"] = model
        bash = Path(os.environ.get("ProgramFiles", "")) / "Git/bin/bash.exe"
        if bash.is_file():
            env["CLAUDE_CODE_GIT_BASH_PATH"] = str(bash)
        (Path(env["CLAUDE_CONFIG_DIR"]) / "settings.json").write_text(
            json.dumps({"autoMemoryEnabled": native}), encoding="utf-8"
        )
        return
    config = (
        Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex"))) / "config.toml"
    )
    source: dict[str, Any] = (
        tomllib.loads(config.read_text(encoding="utf-8-sig")) if config.exists() else {}
    )
    provider = source.get("model_providers", {}).get(source.get("model_provider"), {})
    endpoint = provider.get("base_url") or os.environ.get("OPENAI_BASE_URL")
    secret = (
        os.environ.get(provider.get("env_key", ""))
        or provider.get("experimental_bearer_token")
        or os.environ.get("OPENAI_API_KEY")
    )
    if not endpoint or not secret:
        raise RuntimeError(
            "Codex benchmark needs a configured Responses provider and key; private account files are not copied"
        )
    env["SMEM_BENCH_ROUTE_KEY"] = secret
    lines = [
        'model_provider = "bench"',
        'model_reasoning_effort = "low"',
        'approval_policy = "never"',
        "[features]",
        "plugins = false",
        "apps = false",
        "multi_agent = false",
        f"memories = {str(native).lower()}",
        "[model_providers.bench]",
        'name = "bench"',
        'wire_api = "responses"',
        'env_key = "SMEM_BENCH_ROUTE_KEY"',
        "base_url = " + json.dumps(endpoint),
        "request_max_retries = 1",
        "stream_max_retries = 1",
        "[model_providers.bench.env_http_headers]",
    ]
    for index, (header, value) in enumerate(provider.get("http_headers", {}).items()):
        var = f"SMEM_BENCH_ROUTE_{index}"
        env[var] = value
        lines.append(json.dumps(header) + " = " + json.dumps(var))
    lines += [
        "[shell_environment_policy]",
        'inherit = "all"',
        'exclude = ["SMEM_BENCH_ROUTE_*", "*API_KEY", "*AUTH_TOKEN"]',
        "[projects." + json.dumps(str(repo)) + "]",
        'trust_level = "trusted"',
    ]
    (Path(env["CODEX_HOME"]) / "config.toml").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )
    rules = Path(env["CODEX_HOME"]) / "rules"
    rules.mkdir(exist_ok=True)
    (rules / "smem-bench.rules").write_text(
        'prefix_rule(pattern=["mem"], decision="allow")\nprefix_rule(pattern=["mem.exe"], decision="allow")\n',
        encoding="utf-8",
    )


def ask(
    client: str,
    env: dict[str, str],
    repo: Path,
    prompt: str,
    model: str,
    *,
    native: bool = False,
) -> tuple[str, str]:
    routing(client, env, repo, model, native)
    if client == "claude":
        args = [
            "-p",
            "--model",
            model,
            "--no-session-persistence",
            "--strict-mcp-config",
            "--mcp-config",
            '{"mcpServers":{}}',
            "--tools",
            "Bash,Read,Glob,Grep" + (",Write,Edit" if native else ""),
            "--allowedTools",
            "Bash(mem *)",
            "Bash(mem.exe *)",
            "Read",
            "Glob",
            "Grep",
            "--permission-mode",
            "dontAsk",
            "--max-turns",
            "10",
            "--output-format",
            "json",
        ]
        if native:
            args += ["--allowedTools", "Write", "Edit"]
    else:
        args = [
            "exec",
            "-m",
            model,
            "--sandbox",
            "workspace-write",
            "--json",
            "--color",
            "never",
            "-",
        ]
        if not native:
            args.insert(1, "--ephemeral")
    with (
        tempfile.TemporaryFile() as stdin,
        tempfile.TemporaryFile() as stdout,
        tempfile.TemporaryFile() as stderr,
    ):
        stdin.write((prompt + "\n").encode())
        stdin.seek(0)
        timed_out = False
        try:
            result = subprocess.run(
                [*executable(client), *args],
                check=False,
                cwd=repo,
                env=env,
                stdin=stdin,
                stdout=stdout,
                stderr=stderr,
                timeout=150,
            )
            exit_code = result.returncode
        except subprocess.TimeoutExpired:
            timed_out = True
            exit_code = -1
        stdout.seek(0)
        stderr.seek(0)
        output = stdout.read().decode("utf-8", errors="replace")
        error = stderr.read().decode("utf-8", errors="replace")
    events: list[dict[str, Any]] = []
    for line in output.splitlines():
        try:
            value = json.loads(line)
            if isinstance(value, dict):
                events.append(value)
        except ValueError:
            continue
    if (
        client == "claude"
        and events
        and events[-1].get("type") == "result"
        and (not exit_code or timed_out)
        and not events[-1].get("is_error")
    ):
        return str(events[-1].get("result", "")), "ok"
    if (
        client == "codex"
        and (not exit_code or timed_out)
        and any(event.get("type") == "turn.completed" for event in events)
    ):
        replies = [
            event["item"].get("text", "")
            for event in events
            if event.get("type") == "item.completed"
            and event.get("item", {}).get("type") == "agent_message"
        ]
        return (replies[-1], "ok") if replies else ("", "missing final response")
    if timed_out:
        return "", "timeout"
    message = (output + " " + error).lower()
    for label in [
        "401",
        "403",
        "429",
        "503",
        "502",
        "400",
        "sandbox",
        "authentication",
        "unauthorized",
        "model",
        "max_turns",
        "permission",
    ]:
        if label in message:
            return "", f"client error: {label}"
    return "", f"client exit {exit_code}"
