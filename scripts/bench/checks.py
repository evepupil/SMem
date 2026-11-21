"""Additional controls. These checks do not change the paired handoff score."""

import argparse
import concurrent.futures
import hashlib
import json
import subprocess
import tempfile
from pathlib import Path
from typing import Any

from clients import ask
from handoff import READER, WRITER, calls, field_correct, fixture, scratch, transfer


def native_control(
    client: str, person: str, binaries: Path, model: str
) -> dict[str, Any]:
    with tempfile.TemporaryDirectory(
        prefix="smem-control-", dir=scratch(), ignore_cleanup_errors=True
    ) as temporary:
        repo, env = fixture(Path(temporary), binaries, native=True)
        _, writer = ask(
            client, env, repo, WRITER.format(person=person), model, native=True
        )
        base = Path(
            env["CLAUDE_CONFIG_DIR"] if client == "claude" else env["CODEX_HOME"]
        )
        memory_dirs = (
            list((base / "projects").glob("*/memory"))
            if client == "claude"
            else [base / "memories", base / "memories_v2"]
        )
        produced = any(
            person in note.read_text(encoding="utf-8", errors="replace")
            for folder in memory_dirs
            for note in folder.rglob("*.md")
        )
        workspace_contains = any(
            person in note.read_text(encoding="utf-8", errors="replace")
            for note in repo.rglob("*")
            if note.is_file()
            and ".git" not in note.parts
            and note.stat().st_size < 100_000
        )
        reply, reader = ask(client, env, repo, READER, model, native=True)
        return {
            "client": client,
            "writer": writer,
            "reader": reader,
            "generated_before_read": produced,
            "answer_in_project_files": workspace_contains,
            "used": field_correct(reply, person),
            "delay": "immediate next process",
        }


def exercise(binaries: Path, model: str, cases: dict[str, str]) -> dict[str, Any]:
    old, new = cases["claude-1"], cases["codex-2"]
    neighbor = "维护窗口为每周四早上。"
    with tempfile.TemporaryDirectory(
        prefix="smem-lifecycle-", dir=scratch(), ignore_cleanup_errors=True
    ) as temporary:
        root = Path(temporary)
        source, initial = fixture(root / "seed", binaries)
        payload = next(binaries.glob("payload*"))
        for text in [f"项目的发版放行人为{old}。", neighbor]:
            subprocess.run(
                [str(payload), "add", text],
                cwd=source,
                env=initial,
                check=True,
                capture_output=True,
            )

        def fresh(name: str, parent: Path) -> tuple[Path, dict[str, str]]:
            repo, env = fixture(root / name, binaries)
            transfer(parent, repo)
            return repo, env

        def contains(repo: Path, text: str) -> bool:
            return any(
                text in note.read_text(encoding="utf-8-sig")
                for note in (repo / ".mem/memory").glob("*.md")
            )

        def invoked(repo: Path, target: str, verb: str) -> bool:
            return any(
                call["verb"] == verb and call["ok"] for call in calls(repo, target)
            )

        change, env = fresh("change", source)
        _, correction = ask(
            "codex",
            env,
            change,
            f"更正这个项目的发版约定：今后统一由{new}放行，原来的放行人不再负责。",
            model,
        )
        change_ok = (
            invoked(change, new, "update")
            and contains(change, new)
            and not contains(change, old)
            and contains(change, neighbor)
        )
        correction_evidence = {
            "update_succeeded": invoked(change, new, "update"),
            "contains_new": contains(change, new),
            "contains_old": contains(change, old),
            "neighbor_intact": contains(change, neighbor),
            "same_files": {note.name for note in (source / ".mem/memory").glob("*.md")}
            == {note.name for note in (change / ".mem/memory").glob("*.md")},
        }
        inspect, env = fresh("inspect", change)
        reply, browse = ask(
            "claude",
            env,
            inspect,
            "这个项目有哪些长期约定？列出来，我想核对原文。",
            model,
        )
        browse_ok = (
            any(
                call["verb"] in {"list", "show"} and call["ok"]
                for call in calls(inspect, new)
            )
            and new in reply
            and old not in reply
        )
        forget, env = fresh("forget", change)
        _, deletion = ask(
            "claude",
            env,
            forget,
            "忘掉这个项目关于发版放行人的那条约定，保留其他约定。",
            model,
        )
        forget_ok = (
            invoked(forget, new, "forget")
            and not contains(forget, old)
            and not contains(forget, new)
            and contains(forget, neighbor)
        )
        check, env = fresh("verify", forget)
        final, verify = ask("codex", env, check, READER, model)
        isolated, _ = fixture(root / "other-project", binaries)
        elsewhere, isolate = ask("claude", initial.copy(), isolated, READER, model)
        no_name = old not in final and new not in final and "未确定" in final
        return {
            "correction": correction,
            "changed_in_place": change_ok,
            "correction_evidence": correction_evidence,
            "browse": browse,
            "listed_current": browse_ok,
            "browse_evidence": {
                "new_in_answer": new in reply,
                "old_in_answer": old in reply,
                "neighbor_in_answer": "周四" in reply,
            },
            "deletion": deletion,
            "single_forgotten": forget_ok,
            "reopen": verify,
            "no_forgotten_name": no_name,
            "other_project": isolate,
            "no_cross_project_name": old not in elsewhere
            and new not in elsewhere
            and "未确定" in elsewhere,
            "operations": {
                label: [call["verb"] for call in calls(repo, new)]
                for label, repo in [
                    ("change", change),
                    ("inspect", inspect),
                    ("forget", forget),
                    ("verify", check),
                    ("other", isolated),
                ]
            },
        }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=["native", "lifecycle"])
    parser.add_argument("--bin", type=Path, required=True)
    parser.add_argument("--cases", type=Path, required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    bins = args.bin.resolve()
    cases = json.loads(args.cases.read_text(encoding="utf-8"))
    report: dict[str, Any] = {
        "mode": args.mode,
        "model": args.model,
        "corpus_sha256": hashlib.sha256(args.cases.read_bytes()).hexdigest(),
    }
    if args.out.exists():
        raise SystemExit("Choose a new report file")
    if args.mode == "native":
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as workers:
            jobs = [
                workers.submit(native_control, client, cases[key], bins, args.model)
                for key in cases
                for client in [key.split("-")[0]]
            ]
            report["controls"] = [job.result() for job in jobs]
    else:
        report["result"] = exercise(bins, args.model, cases)
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(
        json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
    )
    print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    main()
