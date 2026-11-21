import json
import tempfile
import unittest
from pathlib import Path

from handoff import calls, field_correct


class MeasurementTest(unittest.TestCase):
    def test_final_field_requires_named_assignee(self) -> None:
        person = "虚构-13579B"
        self.assertTrue(
            field_correct(f"项目：演示\n放行人：{person}\n下一步：提交", person)
        )
        self.assertFalse(field_correct(f"项目：{person}\n放行人：未确定", person))
        self.assertFalse(field_correct(f"放行人：{person}（待确认）", person))

    def test_only_the_note_body_counts_as_a_read(self) -> None:
        person = "虚构-13579B"
        with tempfile.TemporaryDirectory() as temporary:
            repo = Path(temporary)
            journal = repo / ".smem-observations"
            journal.mkdir()
            for index, body in enumerate(
                [f"[项目] note-{person}.md:1", f"项目放行人为{person}"]
            ):
                event = {
                    "args": ["search", "发布"],
                    "code": 0,
                    "time": index,
                    "output": body,
                }
                (journal / f"call-{index}.json").write_text(
                    json.dumps(event), encoding="utf-8"
                )
            self.assertEqual(
                [event["body_hit"] for event in calls(repo, person)], [False, True]
            )
