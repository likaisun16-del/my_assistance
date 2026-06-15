import os
import sys
from unittest.mock import MagicMock

import pytest

PROJECT_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if PROJECT_ROOT not in sys.path:
    sys.path.insert(0, PROJECT_ROOT)

from internal.agent.script_generator import ScriptGenerator, ScriptRequest


def test_generate_returns_structured_script():
    fake_llm = MagicMock()
    fake_llm.chat.return_value = (
        '{"hook":"开头钩子","body":["第一段","第二段"],"cta":"关注我","duration_estimate":120}'
    )
    gen = ScriptGenerator(llm=fake_llm)
    out = gen.generate(ScriptRequest(topic="RAG 入门", duration=120, style="口播"))
    assert out.hook == "开头钩子"
    assert out.body == ["第一段", "第二段"]
    assert out.cta == "关注我"
    assert out.duration_estimate == 120


def test_generate_handles_codeblock_wrapped_json():
    fake_llm = MagicMock()
    fake_llm.chat.return_value = (
        "```json\n"
        '{"hook":"h","body":["b"],"cta":"c","duration_estimate":60}\n'
        "```"
    )
    gen = ScriptGenerator(llm=fake_llm)
    out = gen.generate(ScriptRequest(topic="x", duration=60))
    assert out.hook == "h"
    assert out.duration_estimate == 60


def test_generate_handles_invalid_json():
    fake_llm = MagicMock()
    fake_llm.chat.return_value = "not a json"
    gen = ScriptGenerator(llm=fake_llm)
    with pytest.raises(ValueError, match="invalid script json"):
        gen.generate(ScriptRequest(topic="x", duration=60, style="口播"))
