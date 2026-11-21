"""Paired blind handoffs; credentials and raw client output never enter the report."""

import argparse
import concurrent.futures
import hashlib
import json
import os
import re
import secrets
import shutil
import subprocess
import tempfile
import time
from pathlib import Path
from typing import Any

from clients import ask, environment, executable

READER = "准备一份本项目的上线交接单。在回复中分三行填写：项目、放行人、下一步。没有依据的字段写‘未确定’。"
WRITER = "本项目从现在起有一项长期约定：每次发版都由{person}放行。请落实这项约定。"


def scratch() -> Path:
    selected = os.environ.get("SMEM_BENCH_SCRATCH")
    if not selected:
        raise RuntimeError(
            "Set SMEM_BENCH_SCRATCH to a private scratch directory outside real user and project instruction trees"
        )
    root = Path(selected).resolve()
    root.mkdir(parents=True, exist_ok=True)
    for parent in (root, *root.parents):
        for instruction in (
            "AGENTS.md",
            "AGENTS.override.md",
            "CLAUDE.md",
            "CLAUDE.local.md",
            ".claude/CLAUDE.md",
        ):
            if (parent / instruction).exists():
                raise RuntimeError(
                    f"Ancestor instructions would leak into the benchmark: {parent / instruction}"
                )
    return root


def fixture(
    parent: Path, binaries: Path, *, native: bool = False
) -> tuple[Path, dict[str, str]]:
    repo = parent / "work"
    repo.mkdir(parents=True)
    env = environment(parent / "user", binaries)
    if native:
        env["PATH"] = os.environ["PATH"]
    subprocess.run(
        ["git", "init", "-q", "--template="],
        cwd=repo,
        env=env,
        check=True,
        capture_output=True,
    )
    if not native:
        program = binaries / (
            "payload.exe" if (binaries / "payload.exe").exists() else "payload"
        )
        subprocess.run(
            [str(program), "init", "--agents", "claude,codex"],
            cwd=repo,
            env=env,
            check=True,
            capture_output=True,
        )
    (repo / ".smem-observations").mkdir()
    return repo, env


def transfer(source: Path, target: Path) -> None:
    for note in (source / ".mem/memory").glob("*.md"):
        if note.is_file() and not note.is_symlink():
            shutil.copyfile(note, target / ".mem/memory" / note.name)


def calls(repo: Path, person: str) -> list[dict[str, Any]]:
    events = [
        json.loads(path.read_text(encoding="utf-8"))
        for path in (repo / ".smem-observations").glob("call-*.json")
    ]
    return [
        {
            "verb": event["args"][0] if event["args"] else "",
            "ok": event["code"] == 0,
            "body_hit": any(
                person in line
                and ".md" not in line
                and not line.startswith(("ref=", "ref:"))
                for line in event["output"].splitlines()
            ),
            "query": event["args"][1:6] if event["args"][:1] == ["search"] else [],
        }
        for event in sorted(events, key=lambda value: value["time"])
    ]


def field_correct(answer: str, person: str) -> bool:
    return any(
        person in line
        and re.search(r"放行人.{0,10}[:：|]", line)
        and not any(
            word in line for word in ("未确定", "不确定", "并非", "不是", "待确认")
        )
        for line in answer.splitlines()
    )


def trial(key: str, person: str, bins: Path, model: str) -> dict[str, Any]:
    writer = key.split("-")[0]
    reader = "codex" if writer == "claude" else "claude"
    start = time.monotonic()
    with tempfile.TemporaryDirectory(
        prefix="smem-blind-", dir=scratch(), ignore_cleanup_errors=True
    ) as temporary:
        root = Path(temporary)
        origin, env = fixture(root / "origin", bins)
        _, write_state = ask(writer, env, origin, WRITER.format(person=person), model)
        saved = any(
            person in file.read_text(encoding="utf-8-sig")
            for file in (origin / ".mem/memory").glob("*.md")
        )
        written = calls(origin, person)
        shared = saved and any(call["verb"] == "add" and call["ok"] for call in written)
        target, env = fixture(root / "next", bins)
        transfer(origin, target)
        reply, read_state = ask(reader, env, target, READER, model)
        observed = calls(target, person)
        evidence = any(
            call["verb"] in {"search", "show", "list"}
            and call["ok"]
            and call["body_hit"]
            for call in observed
        )
        used = field_correct(reply, person)
        return {
            "id": key,
            "writer": writer,
            "reader": reader,
            "writer_status": write_state,
            "reader_status": read_state,
            "saved": shared,
            "read": evidence,
            "used": used,
            "pass": shared and evidence and used and write_state == read_state == "ok",
            "write_calls": written,
            "read_calls": observed,
            "seconds": round(time.monotonic() - start, 1),
        }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--bin", type=Path, required=True)
    parser.add_argument("--cases", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--jobs", type=int, default=3)
    parser.add_argument("--rounds", type=int, default=5)
    args = parser.parse_args()
    if args.out.exists():
        raise SystemExit("Choose a new report path; evidence is not overwritten")
    if not args.cases.exists():
        args.cases.parent.mkdir(parents=True, exist_ok=True)
        with args.cases.open("x", encoding="utf-8") as handle:
            json.dump(
                {
                    f"{writer}-{index + 1}": "青岑-" + secrets.token_hex(3).upper()
                    for writer in ["claude", "codex"]
                    for index in range(args.rounds)
                },
                handle,
                ensure_ascii=False,
            )
    cases: dict[str, str] = json.loads(args.cases.read_text(encoding="utf-8"))
    rows: list[dict[str, Any]] = []
    binaries = args.bin.resolve()
    meta = {
        "model": args.model,
        "protocol": "isolated-workspace-no-plugins-v1",
        "runner_sha256": hashlib.sha256(
            Path(__file__).with_name("clients.py").read_bytes()
        ).hexdigest(),
        "writer_prompt": WRITER,
        "reader_prompt": READER,
        "corpus_sha256": hashlib.sha256(args.cases.read_bytes()).hexdigest(),
        "clients": {
            client: subprocess.check_output(
                [*executable(client), "--version"], encoding="utf-8"
            ).strip()
            for client in ["claude", "codex"]
        },
        "payload_sha256": hashlib.sha256(
            next(binaries.glob("payload*")).read_bytes()
        ).hexdigest(),
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as workers:
        jobs = [
            workers.submit(trial, key, person, binaries, args.model)
            for key, person in sorted(
                cases.items(), key=lambda pair: pair[0].split("-")[::-1]
            )
        ]
        for completed in concurrent.futures.as_completed(jobs):
            row = completed.result()
            rows.append(row)
            args.out.write_text(
                json.dumps(
                    {**meta, "trials": sorted(rows, key=lambda item: item["id"])},
                    ensure_ascii=False,
                    indent=2,
                )
                + "\n",
                encoding="utf-8",
            )
            print(
                row["id"],
                row["writer_status"],
                row["reader_status"],
                "saved/read/used",
                row["saved"],
                row["read"],
                row["used"],
                flush=True,
            )


if __name__ == "__main__":
    main()
