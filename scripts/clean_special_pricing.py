#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import math
import os
import re
import sys
import tempfile
from copy import deepcopy
from dataclasses import dataclass
from decimal import Decimal
from pathlib import Path
from typing import Any


SCRIPT_DIR = Path(__file__).resolve().parent
DEFAULT_INPUT = SCRIPT_DIR.parent.parent / "向量特殊规则.txt"
DEFAULT_NORMAL = SCRIPT_DIR.parent.parent / "向量普通规则.txt"
DEFAULT_OUTPUT = SCRIPT_DIR.parent / "tmp" / "special-clean" / "SpecialModelPricing.cleaned.json"
DEFAULT_REPORT = SCRIPT_DIR.parent / "tmp" / "special-clean" / "SpecialModelPricing.report.json"

# These branches are fixed image display tables, not part of the task/special
# pricing catalog previously aligned with SpecialModelPricing.
NON_SPECIAL_DISPLAY_BRANCHES = {
    "aigc-image-gem",
    "aigc-image-hunyuan",
    "aigc-image-qwen",
}


@dataclass(frozen=True)
class CleanResult:
    success: bool
    discovered_models: set[str]
    generated_models: set[str]
    uncovered_models: set[str]
    errors: list[str]


def _load_catalog_module():
    path = SCRIPT_DIR / "import_fzbl_catalog.py"
    spec = importlib.util.spec_from_file_location("_fzbl_catalog_for_cleaner", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"无法加载规则转换器: {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def read_utf8(path: Path) -> str:
    raw = path.read_bytes()
    try:
        text = raw.decode("utf-8-sig")
    except UnicodeDecodeError as exc:
        raise ValueError(f"输入文件不是有效 UTF-8: {path}: {exc}") from exc
    if not text.strip():
        raise ValueError(f"输入文件为空: {path}")
    return text


def read_normal_model_names(path: Path | None) -> set[str] | None:
    if path is None:
        return None
    if not path.exists():
        return None
    text = read_utf8(path)
    try:
        payload = json.loads(text)
    except json.JSONDecodeError as exc:
        raise ValueError(f"普通规则不是有效 JSON: {path}: {exc}") from exc
    names: set[str] = set()

    def walk(value: Any) -> None:
        if isinstance(value, dict):
            model_name = value.get("model_name")
            if isinstance(model_name, str) and model_name.strip():
                names.add(model_name.strip())
            for child in value.values():
                walk(child)
        elif isinstance(value, list):
            for child in value:
                walk(child)

    walk(payload)
    if not names:
        raise ValueError(f"普通规则没有发现 model_name: {path}")
    return names


def discover_special_models(source: str) -> set[str]:
    catalog = _load_catalog_module()
    registered = set(catalog.CANK_SPECIAL_MODELS)
    discovered = {name for name in registered if _quoted_literal_present(source, name)}

    comparison_patterns = (
        r"""else\s+if\s*\(\s*["']([^"']+)["']\s*===\s*[A-Za-z_$][\w$?.]*""",
        r"""else\s+if\s*\(\s*[A-Za-z_$][\w$?.]*\s*===\s*["']([^"']+)["']""",
        r"""["']([^"']+)["']\s*===\s*[A-Za-z_$][\w$?.]*\s*\?\s*[A-Za-z_$][\w$]*\.push\s*\(""",
    )
    for pattern in comparison_patterns:
        discovered.update(re.findall(pattern, source))

    for array_text in re.findall(
        r"""new\s+Set\s*\(\s*\[([^\]]+)\]\s*\)\.has\s*\([^)]*model_name""",
        source,
        flags=re.DOTALL,
    ):
        discovered.update(re.findall(r"""["']([^"']+)["']""", array_text))

    discovered.difference_update(NON_SPECIAL_DISPLAY_BRANCHES)
    return {name for name in discovered if _looks_like_model_name(name)}


def _quoted_literal_present(source: str, value: str) -> bool:
    return f'"{value}"' in source or f"'{value}'" in source


def _looks_like_model_name(value: str) -> bool:
    if not value or len(value) > 100 or any(ch.isspace() for ch in value):
        return False
    return bool(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:-]*", value))


def _display_only_rule(
    title: str,
    description: str,
    rows: list[dict[str, Any]],
    *,
    columns: list[dict[str, str]] | None = None,
) -> dict[str, Any]:
    return {
        "type": "entry_fields",
        "credit_unit_price": 0.05,
        "billing_enabled": False,
        "key_fields": [],
        "entries": [],
        "display_only_reason": "规则已从供应商页面清洗，但缺少可安全绑定的普通目录渠道或价格依赖运行时基础价",
        "display": {
            "title": title,
            "description": description,
            "billing_enabled": False,
            "display_only_reason": "仅展示；未启用真实特殊扣费",
            "sections": [
                {
                    "title": title,
                    "description": description,
                    "columns": columns
                    or [
                        {"key": "description", "title": "规则"},
                        {"key": "price_text", "title": "价格"},
                    ],
                    "rows": rows,
                }
            ],
        },
    }


def _source_only_rules() -> dict[str, dict[str, Any]]:
    return {
        "aigc-video-vidu": _display_only_rule(
            "Vidu 视频生成",
            "价格依赖该聚合模型的运行时基础价，按版本、分辨率和时长换算。",
            [
                {"description": "Q2 / Q2 Pro / Q2 Turbo / Q3 Pro / Q3 Turbo", "price_text": "按秒 × 运行时基础价"},
            ],
        ),
        "aigc-template-effect-vidu": _display_only_rule(
            "Vidu 模板特效",
            "按秒计费，单价由普通目录中的模型基础价提供。",
            [{"description": "模板特效", "price_text": "基础价 / 秒"}],
        ),
        "aigc-video-kling": _display_only_rule(
            "Kling 视频生成",
            "按版本、分辨率、声音、参考视频和时长组合换算，基础价来自普通目录。",
            [{"description": "Kling O1 / 1.6-3.0 / Omni", "price_text": "规格倍率 × 基础价"}],
        ),
        "aigc-video-hailuo": _display_only_rule(
            "Hailuo 视频生成",
            "Hailuo 02、2.3 和 2.3-Fast 按分辨率与时长换算。",
            [
                {"description": "02 / 2.3，768P 或 1080P", "price_text": "规格倍率 × 基础价"},
                {"description": "2.3-Fast，768P 或 1080P", "price_text": "规格倍率 × 基础价"},
            ],
        ),
        "MiniMax-Hailuo-2.3-Fast": _display_only_rule(
            "Hailuo 2.3 Fast",
            "图生视频固定规格价格。",
            [
                {"description": "768P / 6s", "price_text": "1 × 基础价"},
                {"description": "768P / 10s", "price_text": "225/135 × 基础价"},
                {"description": "1080P / 6s", "price_text": "231/135 × 基础价"},
            ],
        ),
        "alibailian-video": _display_only_rule(
            "阿里百炼视频",
            "480P、720P、1080P，支持 5 秒和 10 秒。",
            [
                {"description": "480P", "price_text": "0.3 × 秒数 × 基础价"},
                {"description": "720P", "price_text": "0.6 × 秒数 × 基础价"},
                {"description": "1080P", "price_text": "1.0 × 秒数 × 基础价"},
            ],
        ),
        "doubao-seedance-2-0-260128": _display_only_rule(
            "Doubao Seedance 2.0",
            "按 1M tokens 计费，含参考视频使用换算价。",
            [
                {"description": "不含参考视频", "price_text": "基础价 / 1M tokens"},
                {"description": "含参考视频", "price_text": "基础价 × 2800/4600 / 1M tokens"},
            ],
        ),
        "doubao-seedance-2-0-fast-260128": _display_only_rule(
            "Doubao Seedance 2.0 Fast",
            "按 1M tokens 计费，含参考视频使用换算价。",
            [
                {"description": "不含参考视频", "price_text": "基础价 / 1M tokens"},
                {"description": "含参考视频", "price_text": "基础价 × 2200/3700 / 1M tokens"},
            ],
        ),
        "jimeng-videos": _display_only_rule(
            "即梦视频",
            "jimengv3 文生视频和图生视频按时长计费。",
            [
                {"description": "文生/图生 5s", "price_text": "1 × 基础价"},
                {"description": "文生/图生 10s", "price_text": "2 × 基础价"},
            ],
        ),
        "kling-kolors-virtual-try-on": _display_only_rule(
            "Kling 虚拟试穿",
            "AI 虚拟试穿按次使用普通目录基础价。",
            [{"description": "虚拟试穿", "price_text": "基础价 / 次"}],
        ),
        "pixverse-upload": _display_only_rule(
            "PixVerse 素材上传",
            "image/upload 与 media/upload 免费。",
            [{"description": "素材上传", "price_text": "免费"}],
        ),
    }


def build_special_pricing(discovered_models: set[str], credit_unit_price: Decimal) -> dict[str, Any]:
    catalog = _load_catalog_module()
    rows = [
        {
            "model_name": name,
            "description": "",
            "tags": "",
            "supported_endpoint_types": [],
        }
        for name in sorted(discovered_models)
    ]
    document = catalog.build_special_pricing(rows, credit_unit_price)
    models = document.setdefault("models", {})
    for model_name, rule in _source_only_rules().items():
        if model_name in discovered_models and model_name not in models:
            rule["credit_unit_price"] = float(credit_unit_price)
            models[model_name] = rule
    _copy_alias_rule(models, discovered_models, "gemini-3-pro-image-preview", "gemini-3-pro-image")
    _copy_alias_rule(models, discovered_models, "gemini-3.1-flash-image-preview", "gemini-3.1-flash-image")
    return document


def _copy_alias_rule(models: dict[str, Any], discovered: set[str], source_name: str, alias_name: str) -> None:
    if alias_name not in discovered or alias_name in models or source_name not in models:
        return
    models[alias_name] = deepcopy(models[source_name])


def _set_minimum_price(rule: dict[str, Any]) -> None:
    display = rule.get("display")
    if not isinstance(display, dict):
        return
    candidates: list[float] = []
    credit = _finite_number(rule.get("credit_unit_price")) or 0.05

    multipliers = rule.get("multipliers")
    if isinstance(multipliers, dict):
        for value in multipliers.values():
            number = _finite_number(value)
            if number is not None and number >= 0:
                candidates.append(number * credit)

    entries = rule.get("entries")
    if isinstance(entries, list):
        for entry in entries:
            if not isinstance(entry, dict):
                continue
            for key in ("price", "first_second_price", "second_second_price", "next_second_price"):
                number = _finite_number(entry.get(key))
                if number is not None and number >= 0:
                    candidates.append(number)

    for section in display.get("sections") or []:
        if not isinstance(section, dict):
            continue
        for row in section.get("rows") or []:
            if not isinstance(row, dict):
                continue
            number = _finite_number(row.get("price"))
            if number is not None and number >= 0:
                candidates.append(number)
            multiplier = _finite_number(row.get("multiplier"))
            if multiplier is not None and multiplier >= 0:
                candidates.append(multiplier * credit)

    if candidates:
        minimum = min(candidates)
        display["min_price"] = round(minimum, 8)
        display["min_price_unit"] = display.get("unit") or "次"
        display["min_price_text"] = f"¥{minimum:.4f} 起"
    elif not rule.get("billing_enabled"):
        display.setdefault("min_price_text", "按规格计费")


def _finite_number(value: Any) -> float | None:
    if isinstance(value, bool):
        return None
    if isinstance(value, (int, float, Decimal)):
        number = float(value)
        if math.isfinite(number):
            return number
    return None


def validate_document(document: dict[str, Any], discovered_models: set[str]) -> list[str]:
    errors: list[str] = []
    models = document.get("models")
    if not isinstance(models, dict):
        return ["输出缺少 models 对象"]

    generated = set(models)
    for name in sorted(discovered_models - generated):
        errors.append(f"发现特殊模型但没有转换器: {name}")
    for name in sorted(generated - discovered_models):
        errors.append(f"生成了源码中未发现的特殊模型: {name}")

    for model_name, rule in sorted(models.items()):
        if not isinstance(rule, dict):
            errors.append(f"{model_name}: 规则不是 JSON 对象")
            continue
        if not isinstance(rule.get("type"), str) or not rule["type"].strip():
            errors.append(f"{model_name}: 缺少规则 type")
        if not isinstance(rule.get("billing_enabled"), bool):
            errors.append(f"{model_name}: billing_enabled 必须是布尔值")
        if not rule.get("billing_enabled") and not rule.get("display_only_reason"):
            errors.append(f"{model_name}: 未启用扣费时必须提供 display_only_reason")
        credit = _finite_number(rule.get("credit_unit_price"))
        if credit is None or credit <= 0:
            errors.append(f"{model_name}: credit_unit_price 必须是正数")
        display = rule.get("display")
        if not isinstance(display, dict):
            errors.append(f"{model_name}: 缺少 display")
            continue
        rows = list(display.get("rows") or [])
        for section in display.get("sections") or []:
            if isinstance(section, dict):
                rows.extend(section.get("rows") or [])
        if not rows:
            errors.append(f"{model_name}: display 没有价格行")
        _set_minimum_price(rule)
        if not display.get("min_price_text"):
            errors.append(f"{model_name}: 无法计算或描述最低价格")
    return errors


def atomic_write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
    fd, temp_name = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temp_name, path)
    except Exception:
        try:
            os.unlink(temp_name)
        except FileNotFoundError:
            pass
        raise


def clean_special_pricing(
    *,
    input_path: Path,
    normal_path: Path | None = DEFAULT_NORMAL,
    output_path: Path,
    report_path: Path,
    credit_unit_price: Decimal = Decimal("0.05"),
) -> CleanResult:
    errors: list[str] = []
    raw_discovered: set[str] = set()
    discovered: set[str] = set()
    filtered_out: set[str] = set()
    generated: set[str] = set()
    source_hash = ""
    document: dict[str, Any] | None = None

    try:
        source = read_utf8(input_path)
        source_hash = hashlib.sha256(source.encode("utf-8")).hexdigest()
        raw_discovered = discover_special_models(source)
        discovered = set(raw_discovered)
        normal_models = read_normal_model_names(normal_path)
        if normal_models is not None:
            filtered_out = discovered - normal_models
            discovered.intersection_update(normal_models)
        if not discovered:
            errors.append("没有发现任何特殊模型分支")
        document = build_special_pricing(discovered, credit_unit_price)
        generated = set((document.get("models") or {}).keys())
        errors.extend(validate_document(document, discovered))
    except Exception as exc:
        errors.append(str(exc))

    uncovered = discovered - generated
    success = not errors and bool(discovered) and document is not None
    report = {
        "status": "success" if success else "blocked",
        "input": str(input_path.resolve()),
        "output": str(output_path.resolve()),
        "source_sha256": source_hash,
        "credit_unit_price": str(credit_unit_price),
        "raw_discovered_count": len(raw_discovered),
        "discovered_count": len(discovered),
        "generated_count": len(generated),
        "raw_discovered_models": sorted(raw_discovered),
        "discovered_models": sorted(discovered),
        "filtered_out_models": sorted(filtered_out),
        "generated_models": sorted(generated),
        "uncovered_models": sorted(uncovered),
        "errors": errors,
    }
    atomic_write_json(report_path, report)
    if success:
        atomic_write_json(output_path, document)

    return CleanResult(
        success=success,
        discovered_models=discovered,
        generated_models=generated,
        uncovered_models=uncovered,
        errors=errors,
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="严格清洗供应商压缩 JS 特殊规则为 SpecialModelPricing JSON。"
    )
    parser.add_argument("--input", type=Path, default=DEFAULT_INPUT)
    parser.add_argument("--normal", type=Path, default=DEFAULT_NORMAL)
    parser.add_argument(
        "--no-normal-filter",
        action="store_true",
        help="不按普通规则 model_name 过滤特殊规则；仅用于调试完整特殊源码覆盖。",
    )
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--report", type=Path, default=DEFAULT_REPORT)
    parser.add_argument("--credit-unit-price", type=Decimal, default=Decimal("0.05"))
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    result = clean_special_pricing(
        input_path=args.input,
        normal_path=None if args.no_normal_filter else args.normal,
        output_path=args.output,
        report_path=args.report,
        credit_unit_price=args.credit_unit_price,
    )
    summary = {
        "success": result.success,
        "discovered": len(result.discovered_models),
        "generated": len(result.generated_models),
        "uncovered": sorted(result.uncovered_models),
        "errors": result.errors,
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0 if result.success else 1


if __name__ == "__main__":
    raise SystemExit(main())
