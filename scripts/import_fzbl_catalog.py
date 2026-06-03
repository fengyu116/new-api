#!/usr/bin/env python3
"""
Import and normalize an fzbl/new-api catalog.

Default mode is dry-run: it writes a report and SQL file but does not touch the
database. Use --apply to pipe the generated SQL into the postgres container.
"""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import os
import re
import subprocess
import sys
import time
from dataclasses import dataclass, field
from decimal import Decimal, ROUND_HALF_UP
from pathlib import Path
from typing import Any


CHANNEL_OPENAI = 1
CHANNEL_ANTHROPIC = 14
CHANNEL_ALI = 17
CHANNEL_KLING = 50
CHANNEL_VIDU = 52
CHANNEL_DOUBAO_VIDEO = 54
CHANNEL_SORA = 55

SYNC_TAG = "fzbl-sync"
SPECIAL_TAG_PREFIX = "fzbl-special"
DEFAULT_BASE_URL = "https://q.aibaotui.com"


@dataclass
class CatalogModel:
    name: str
    upstream_name: str
    description: str
    tags: str
    vendor_id: int
    endpoints: list[str]
    endpoint_map: dict[str, Any]
    groups: list[str]
    quota_type: int
    model_ratio: Decimal
    model_price: Decimal
    completion_ratio: Decimal
    cache_ratio: Decimal | None = None
    channel_type: int = CHANNEL_OPENAI
    model_mapping: dict[str, str] = field(default_factory=dict)
    param_override: dict[str, Any] = field(default_factory=dict)
    is_virtual: bool = False
    source_rule: str = ""


def dec(value: Any, default: str = "0") -> Decimal:
    if value is None or value == "":
        return Decimal(default)
    return Decimal(str(value))


def money(value: Decimal) -> Decimal:
    return value.quantize(Decimal("0.000001"), rounding=ROUND_HALF_UP)


def sql_str(value: Any) -> str:
    if value is None:
        return "NULL"
    return "'" + str(value).replace("'", "''") + "'"


def sql_json(value: Any) -> str:
    return sql_str(json.dumps(value, ensure_ascii=False, separators=(",", ":")))


def slug(value: str) -> str:
    value = value.lower().replace(" ", "")
    value = value.replace("/", "-").replace("x", "x")
    value = re.sub(r"[^a-z0-9._-]+", "-", value)
    return value.strip("-")


def unique_ordered(values: list[str]) -> list[str]:
    seen: set[str] = set()
    out: list[str] = []
    for value in values:
        value = str(value).strip()
        if value and value not in seen:
            seen.add(value)
            out.append(value)
    return out


def channel_type_for_model(model: dict[str, Any]) -> int:
    name = str(model.get("model_name", ""))
    endpoints = ",".join(model.get("supported_endpoint_types") or [])
    if name.startswith("vidu") or "vidu" in endpoints.lower():
        return CHANNEL_VIDU
    if name.startswith("kling-") or "kling" in endpoints.lower():
        return CHANNEL_KLING
    if name.startswith("sora-"):
        return CHANNEL_SORA
    if name.startswith("doubao-seedance"):
        return CHANNEL_DOUBAO_VIDEO
    if name.startswith("wan") or name.startswith("alibailian"):
        return CHANNEL_ALI
    if "anthropic" in endpoints.lower():
        return CHANNEL_ANTHROPIC
    return CHANNEL_OPENAI


def base_model(row: dict[str, Any], endpoint_catalog: dict[str, Any]) -> CatalogModel:
    endpoint_names = unique_ordered(row.get("supported_endpoint_types") or [])
    return CatalogModel(
        name=row["model_name"],
        upstream_name=row["model_name"],
        description=row.get("description") or "",
        tags=row.get("tags") or "",
        vendor_id=int(row.get("vendor_id") or 0),
        endpoints=endpoint_names,
        endpoint_map={name: endpoint_catalog[name] for name in endpoint_names if name in endpoint_catalog},
        groups=unique_ordered(row.get("enable_groups") or ["default"]),
        quota_type=int(row.get("quota_type") or 0),
        model_ratio=dec(row.get("model_ratio")),
        model_price=dec(row.get("model_price")),
        completion_ratio=dec(row.get("completion_ratio")),
        cache_ratio=dec(row.get("cache_ratio")) if row.get("cache_ratio") is not None else None,
        channel_type=channel_type_for_model(row),
    )


def clone_virtual(
    base: CatalogModel,
    suffix_parts: list[str],
    credit_multiplier: Decimal,
    credit_unit_price: Decimal,
    params: dict[str, Any],
    rule: str,
    description_bits: list[str],
) -> CatalogModel:
    item = copy.deepcopy(base)
    item.name = f"{base.name}__{'__'.join(slug(part) for part in suffix_parts)}"
    item.upstream_name = base.name
    item.model_mapping = {item.name: base.name}
    item.param_override = {
        "operations": [
            {"op": "set", "path": key, "value": value}
            for key, value in sorted(params.items())
        ]
    }
    item.model_price = money(credit_unit_price * credit_multiplier)
    item.model_ratio = Decimal("0")
    item.is_virtual = True
    item.source_rule = rule
    extra = " / ".join(description_bits)
    item.description = f"{base.description}\n\nVirtual spec: {extra}".strip()
    item.tags = unique_csv(base.tags, "virtual", "fixed-spec")
    return item


def unique_csv(*parts: str) -> str:
    values: list[str] = []
    for part in parts:
        values.extend([x.strip() for x in str(part or "").split(",")])
    return ",".join(unique_ordered(values))


def vidu_rules(base: CatalogModel, credit_unit_price: Decimal) -> list[CatalogModel]:
    table: dict[str, list[tuple[str, Decimal]]] = {
        "viduq3-pro": [("1080p", Decimal("32")), ("720p", Decimal("30")), ("540p", Decimal("14"))],
        "viduq3-turbo": [("1080p", Decimal("16")), ("720p", Decimal("12")), ("540p", Decimal("8"))],
        "viduq3": [("1080p", Decimal("25")), ("720p", Decimal("20")), ("540p", Decimal("10"))],
        "viduq3-mix": [("1080p", Decimal("30")), ("720p", Decimal("25"))],
    }
    if base.name not in table:
        return []
    out: list[CatalogModel] = []
    for resolution, per_second in table[base.name]:
        for duration in (5, 10, 15):
            out.append(
                clone_virtual(
                    base,
                    [resolution, f"{duration}s"],
                    per_second * Decimal(duration),
                    credit_unit_price,
                    {"resolution": resolution, "duration": duration},
                    "cank:vidu-q3",
                    [resolution, f"{duration}s", f"{per_second}/s"],
                )
            )
    return out


def sora_rules(base: CatalogModel, credit_unit_price: Decimal) -> list[CatalogModel]:
    if base.name == "sora-2":
        rows = [("720x1280", d, Decimal(d)) for d in (4, 8, 12)]
    elif base.name == "sora-2-pro":
        rows = []
        for duration in (4, 8, 12):
            rows.append(("720x1280", duration, Decimal(duration)))
            rows.append(("1024x1792", duration, Decimal("1.666667") * Decimal(duration)))
    else:
        return []
    out = []
    for resolution, duration, multiplier in rows:
        out.append(
            clone_virtual(
                base,
                [resolution, f"{duration}s"],
                multiplier,
                credit_unit_price,
                {"size": resolution, "duration": duration},
                "cank:sora-2",
                [resolution, f"{duration}s"],
            )
        )
    return out


def kling_video_rules(base: CatalogModel, credit_unit_price: Decimal) -> list[CatalogModel]:
    if base.name != "kling-video":
        return []
    rows: list[tuple[str, str, int, Decimal, dict[str, Any]]] = [
        ("kling-v1", "std", 5, Decimal("1"), {}),
        ("kling-v1", "std", 10, Decimal("2"), {}),
        ("kling-v1", "pro", 5, Decimal("3.5"), {}),
        ("kling-v1", "pro", 10, Decimal("7"), {}),
        ("kling-v1-5", "std", 5, Decimal("2"), {}),
        ("kling-v1-5", "std", 10, Decimal("4"), {}),
        ("kling-v1-5", "pro", 5, Decimal("3.5"), {}),
        ("kling-v1-5", "pro", 10, Decimal("7"), {}),
        ("kling-v1-6", "std", 5, Decimal("2"), {}),
        ("kling-v1-6", "std", 10, Decimal("4"), {}),
        ("kling-v1-6", "pro", 5, Decimal("3.5"), {}),
        ("kling-v1-6", "pro", 10, Decimal("7"), {}),
        ("kling-v2-master", "std", 5, Decimal("10"), {}),
        ("kling-v2-master", "std", 10, Decimal("20"), {}),
        ("kling-v2-1", "std", 5, Decimal("2"), {}),
        ("kling-v2-1", "std", 10, Decimal("4"), {}),
        ("kling-v2-1", "pro", 5, Decimal("3.5"), {}),
        ("kling-v2-1", "pro", 10, Decimal("7"), {}),
        ("kling-v2-1-master", "master", 5, Decimal("10"), {}),
        ("kling-v2-1-master", "master", 10, Decimal("20"), {}),
        ("kling-v2-5-turbo", "std", 5, Decimal("1.5"), {}),
        ("kling-v2-5-turbo", "std", 10, Decimal("3"), {}),
        ("kling-v2-5-turbo", "pro", 5, Decimal("2.5"), {}),
        ("kling-v2-5-turbo", "pro", 10, Decimal("5"), {}),
        ("kling-v2-6", "std", 5, Decimal("1.5"), {"with_audio": False}),
        ("kling-v2-6", "std", 10, Decimal("3"), {"with_audio": False}),
        ("kling-v2-6", "pro", 5, Decimal("2.5"), {"with_audio": False}),
        ("kling-v2-6", "pro", 10, Decimal("5"), {"with_audio": False}),
        ("kling-v2-6", "pro-audio", 5, Decimal("5"), {"with_audio": True}),
        ("kling-v2-6", "pro-audio", 10, Decimal("10"), {"with_audio": True}),
        ("kling-v2-6", "pro-audio-voice", 5, Decimal("6"), {"with_audio": True, "voice_id_required": True}),
        ("kling-v2-6", "pro-audio-voice", 10, Decimal("12"), {"with_audio": True, "voice_id_required": True}),
    ]
    out = []
    for version, mode, duration, multiplier, extra in rows:
        params = {"model_name": version, "mode": mode.replace("-audio", "").replace("-voice", ""), "duration": duration}
        params.update(extra)
        out.append(
            clone_virtual(
                base,
                [version, mode, f"{duration}s"],
                multiplier,
                credit_unit_price,
                params,
                "cank:kling-video",
                [version, mode, f"{duration}s"],
            )
        )
    return out


def pixverse_video_rules(base: CatalogModel, credit_unit_price: Decimal) -> list[CatalogModel]:
    if base.name != "pixverse-video":
        return []
    rows: list[tuple[str, str, int, str, Decimal]] = []
    c1 = {"360p": (6, 8), "540p": (8, 10), "720p": (10, 13), "1080p": (19, 24)}
    v6 = {"360p": (5, 7), "540p": (7, 9), "720p": (9, 12), "1080p": (18, 23)}
    for version, table in (("c1", c1), ("v6", v6)):
        for quality, (no_audio, with_audio) in table.items():
            rows.append((version, quality, 1, "no-audio", Decimal(no_audio)))
            rows.append((version, quality, 1, "audio", Decimal(with_audio)))
    v5 = {"360p": {5: 45, 8: 90}, "540p": {5: 45, 8: 90}, "720p": {5: 60, 8: 120}, "1080p": {5: 120, 8: 240}}
    for quality, durations in v5.items():
        for duration, multiplier in durations.items():
            rows.append(("v5", quality, duration, "normal", Decimal(multiplier)))
            if duration == 5:
                rows.append(("v4-fast", quality, duration, "fast", Decimal(multiplier * 2)))
    out = []
    for version, quality, duration, mode, multiplier in rows:
        params = {
            "version": version,
            "quality": quality,
            "duration": duration,
            "mode": mode,
            "with_audio": mode == "audio",
        }
        out.append(
            clone_virtual(
                base,
                [version, quality, f"{duration}s", mode],
                multiplier,
                credit_unit_price,
                params,
                "cank:pixverse-video",
                [version, quality, f"{duration}s", mode],
            )
        )
    return out


SPECIAL_RULES = [vidu_rules, sora_rules, kling_video_rules, pixverse_video_rules]


def expand_models(
    rows: list[dict[str, Any]],
    endpoint_catalog: dict[str, Any],
    credit_unit_price: Decimal,
    split_special: bool,
) -> tuple[list[CatalogModel], list[CatalogModel], list[dict[str, Any]]]:
    models: list[CatalogModel] = []
    cleanup_virtuals: list[CatalogModel] = []
    skipped: list[dict[str, Any]] = []
    for row in rows:
        base = base_model(row, endpoint_catalog)
        virtuals: list[CatalogModel] = []
        for rule in SPECIAL_RULES:
            virtuals.extend(rule(base, credit_unit_price))
        cleanup_virtuals.extend(virtuals)
        if virtuals and split_special:
            models.extend(virtuals)
        else:
            models.append(base)
            if virtuals:
                skipped.append({"model_name": base.name, "reason": "kept as original model; frontend special pricing table required"})
            elif looks_special(base.name):
                skipped.append({"model_name": base.name, "reason": "no stable v1 split rule"})
    return models, cleanup_virtuals, skipped


def looks_special(name: str) -> bool:
    prefixes = (
        "kling-",
        "pixverse-",
        "vidu",
        "sora-2",
        "happyhorse",
        "wan2.",
        "doubao-seedance",
        "MiniMax-Hailuo",
        "audio",
    )
    return name.startswith(prefixes)


def price_text(value: Decimal | float | int) -> str:
    return f"💰{Decimal(str(value)).quantize(Decimal('0.0001'), rounding=ROUND_HALF_UP):f}"


def min_price_from_sections(sections: list[dict[str, Any]]) -> Decimal:
    prices: list[Decimal] = []
    for section in sections:
        for row in section.get("rows") or []:
            if row.get("price") is not None:
                prices.append(dec(row.get("price")))
            elif row.get("first_second_price") is not None:
                prices.append(dec(row.get("first_second_price")))
            elif row.get("multiplier") is not None:
                unit = dec(section.get("credit_unit_price"), "0.05")
                prices.append(unit * dec(row.get("multiplier")))
    return min(prices) if prices else Decimal("0")


def display_config(
    title: str,
    description: str,
    unit: str,
    credit_unit_price: Decimal,
    billing_enabled: bool,
    sections: list[dict[str, Any]],
    display_only_reason: str = "",
) -> dict[str, Any]:
    min_price = min_price_from_sections(sections)
    first = sections[0] if sections else {}
    return {
        "title": title,
        "description": description,
        "unit": unit,
        "credit_unit_price": float(credit_unit_price),
        "billing_enabled": billing_enabled,
        "display_only_reason": display_only_reason,
        "columns": first.get("columns") or [],
        "rows": first.get("rows") or [],
        "sections": sections,
        "min_price": float(min_price),
        "min_price_unit": unit,
        "min_price_text": f"{price_text(min_price)} 起" if min_price > 0 else "",
    }


def direct_display_only_rule(row: dict[str, Any], credit_unit_price: Decimal, reason: str) -> dict[str, Any]:
    price = dec(row.get("model_price"), "0")
    if price <= 0:
        price = credit_unit_price * dec(row.get("model_ratio"), "1")
    unit = "次" if int(row.get("quota_type") or 1) == 1 else "单位"
    sections = [
        {
            "title": "特殊价格",
            "description": reason,
            "unit": unit,
            "columns": [
                {"key": "description", "title": "说明"},
                {"key": "price", "title": "价格"},
            ],
            "rows": [
                {
                    "description": "cank 中存在特殊展示/计费分支；当前仅展示兼容价格，未启用动态特殊扣费",
                    "price": float(price),
                    "price_text": price_text(price),
                    "unit": unit,
                }
            ],
        }
    ]
    return {
        "type": "display_only",
        "credit_unit_price": float(credit_unit_price),
        "billing_enabled": False,
        "display_only_reason": reason,
        "display": display_config("特殊价格", reason, unit, credit_unit_price, False, sections, reason),
    }


def viduq2_rule(credit_unit_price: Decimal) -> dict[str, Any]:
    video_rows = [
        ("text", "文生", "Q2", "540p", Decimal("0.5000"), Decimal("0.1000")),
        ("text", "文生", "Q2", "720p", Decimal("0.7500"), Decimal("0.2500")),
        ("text", "文生", "Q2", "1080p", Decimal("1.0000"), Decimal("0.5000")),
        ("reference", "参考生", "Q2", "540p", Decimal("0.7500"), Decimal("0.2500")),
        ("reference", "参考生", "Q2", "720p", Decimal("1.2500"), Decimal("0.2500")),
        ("reference", "参考生", "Q2", "1080p", Decimal("3.7500"), Decimal("0.5000")),
    ]
    image_rows = [
        ("text", "文生图", "0", "1080p", Decimal("0.3000")),
        ("text", "文生图", "0", "2k", Decimal("0.4000")),
        ("text", "文生图", "0", "4k", Decimal("0.5000")),
        ("reference", "参考生图", "1-3", "1080p", Decimal("0.4000")),
        ("reference", "参考生图", "1-3", "2k", Decimal("0.6000")),
        ("reference", "参考生图", "1-3", "4k", Decimal("1.0000")),
        ("reference", "参考生图", "4-7", "1080p", Decimal("0.5000")),
        ("reference", "参考生图", "4-7", "2k", Decimal("0.8000")),
        ("reference", "参考生图", "4-7", "4k", Decimal("1.5000")),
    ]
    entries = [
        {
            "key": f"video|{ability}|{resolution}",
            "first_second_price": float(first),
            "next_second_price": float(next_second),
            "unit": "次",
            "addons": {
                "with_audio": 0.75,
                "audio": 0.75,
                "recommend_prompt": 0.5,
                "prompt_optimizer": 0.5,
                "enhance_prompt": 0.5,
            },
        }
        for ability, _, _, resolution, first, next_second in video_rows
    ]
    entries.extend(
        {
            "key": f"image|{ability}|{image_count}|{resolution}",
            "price": float(price),
            "unit": "次",
        }
        for ability, _, image_count, resolution, price in image_rows
    )
    sections = [
        {
            "title": "视频生成",
            "description": "图生/参考生音视频直出额外 💰0.7500；启用推荐提示词额外 💰0.5000",
            "unit": "次",
            "columns": [
                {"key": "ability", "title": "能力"},
                {"key": "model", "title": "模型"},
                {"key": "resolution", "title": "分辨率"},
                {"key": "price", "title": "定价"},
            ],
            "rows": [
                {
                    "ability": label,
                    "model": model,
                    "resolution": resolution.upper(),
                    "first_second_price": float(first),
                    "next_second_price": float(next_second),
                    "price_text": f"第1秒{price_text(first)}，后续每秒+{price_text(next_second)}",
                    "unit": "次",
                }
                for _, label, model, resolution, first, next_second in video_rows
            ],
        },
        {
            "title": "图像生成",
            "unit": "次",
            "columns": [
                {"key": "ability", "title": "能力"},
                {"key": "input_images", "title": "输入图片数量"},
                {"key": "resolution", "title": "分辨率"},
                {"key": "price", "title": "价格"},
            ],
            "rows": [
                {
                    "ability": label,
                    "input_images": image_count,
                    "resolution": resolution.upper(),
                    "price": float(price),
                    "price_text": price_text(price),
                    "unit": "次",
                }
                for _, label, image_count, resolution, price in image_rows
            ],
        },
    ]
    return {
        "type": "vidu_q2",
        "credit_unit_price": float(credit_unit_price),
        "billing_enabled": True,
        "default_duration": 5,
        "min_duration": 1,
        "max_duration": 16,
        "default_value": "1080p",
        "entries": entries,
        "display": display_config("分组价格", "按 Vidu Q2 视频/图像规格计费", "次", credit_unit_price, True, sections),
    }


def build_special_pricing(rows: list[dict[str, Any]], credit_unit_price: Decimal) -> dict[str, Any]:
    source_names = {row.get("model_name") for row in rows}
    row_by_name = {row.get("model_name"): row for row in rows}
    unit = float(credit_unit_price)
    special: dict[str, Any] = {"version": "1", "models": {}}

    vidu_tables: dict[str, dict[str, Decimal]] = {
        "viduq3-pro": {"1080p": Decimal("32"), "720p": Decimal("30"), "540p": Decimal("14")},
        "viduq3-turbo": {"1080p": Decimal("16"), "720p": Decimal("12"), "540p": Decimal("8")},
        "viduq3": {"1080p": Decimal("25"), "720p": Decimal("20"), "540p": Decimal("10")},
        "viduq3-mix": {"1080p": Decimal("30"), "720p": Decimal("25")},
    }
    for model_name, table in vidu_tables.items():
        if model_name not in source_names:
            continue
        special["models"][model_name] = {
            "type": "resolution_seconds",
            "credit_unit_price": unit,
            "billing_enabled": True,
            "default_duration": 5,
            "min_duration": 1,
            "max_duration": 16,
            "default_value": "1080p",
            "multipliers": {resolution: float(multiplier) for resolution, multiplier in table.items()},
            "display": {
                "title": "分组价格",
                "description": "时长 1-16 秒，默认 5 秒",
                "unit": "秒",
                "credit_unit_price": unit,
                "billing_enabled": True,
                "columns": [
                    {"key": "ability", "title": "能力"},
                    {"key": "resolution", "title": "清晰度"},
                    {"key": "price", "title": "每秒价格"},
                ],
                "rows": [
                    {
                        "ability": "图生视频 · 文生视频 · 首尾帧",
                        "resolution": resolution.upper(),
                        "multiplier": float(multiplier),
                        "unit": "秒",
                    }
                    for resolution, multiplier in table.items()
                ],
            },
        }

    if "viduq2" in source_names:
        special["models"]["viduq2"] = viduq2_rule(credit_unit_price)

    if "sora-2" in source_names:
        special["models"]["sora-2"] = {
            "type": "sora_seconds_size",
            "credit_unit_price": unit,
            "billing_enabled": True,
            "default_duration": 4,
            "min_duration": 4,
            "max_duration": 12,
            "default_value": "720x1280",
            "multipliers": {"720x1280": 1},
            "display": {
                "title": "视频时长价格",
                "description": "按秒计费，支持 720x1280 / 1280x720",
                "unit": "秒",
                "credit_unit_price": unit,
                "billing_enabled": True,
                "columns": [
                    {"key": "resolution", "title": "尺寸"},
                    {"key": "price", "title": "每秒价格"},
                ],
                "rows": [{"resolution": "720x1280 / 1280x720", "multiplier": 1, "unit": "秒"}],
            },
        }
    if "sora-2-pro" in source_names:
        special["models"]["sora-2-pro"] = {
            "type": "sora_seconds_size",
            "credit_unit_price": unit,
            "billing_enabled": True,
            "default_duration": 4,
            "min_duration": 4,
            "max_duration": 12,
            "default_value": "720x1280",
            "multipliers": {"720x1280": 1, "1024x1792": 1.666667},
            "display": {
                "title": "视频时长价格",
                "description": "按秒计费，高分辨率使用 1.666667 倍率",
                "unit": "秒",
                "credit_unit_price": unit,
                "billing_enabled": True,
                "columns": [
                    {"key": "resolution", "title": "尺寸"},
                    {"key": "price", "title": "每秒价格"},
                ],
                "rows": [
                    {"resolution": "720x1280 / 1280x720", "multiplier": 1, "unit": "秒"},
                    {"resolution": "1024x1792 / 1792x1024", "multiplier": 1.666667, "unit": "秒"},
                ],
            },
        }

    if "kling-video" in source_names:
        kling_rows = [
            ("kling-v1", "std", Decimal("0.2")),
            ("kling-v1", "pro", Decimal("0.7")),
            ("kling-v1-5", "std", Decimal("0.4")),
            ("kling-v1-5", "pro", Decimal("0.7")),
            ("kling-v1-6", "std", Decimal("0.4")),
            ("kling-v1-6", "pro", Decimal("0.7")),
            ("kling-v2-master", "std", Decimal("2")),
            ("kling-v2-1", "std", Decimal("0.4")),
            ("kling-v2-1", "pro", Decimal("0.7")),
            ("kling-v2-1-master", "master", Decimal("2")),
            ("kling-v2-5-turbo", "std", Decimal("0.3")),
            ("kling-v2-5-turbo", "pro", Decimal("0.5")),
            ("kling-v2-6", "std", Decimal("0.3")),
            ("kling-v2-6", "pro", Decimal("0.5")),
            ("kling-v2-6", "pro-audio", Decimal("1")),
            ("kling-v2-6", "pro-audio-voice", Decimal("1.2")),
            ("kling-video-o1", "std", Decimal("0.6")),
            ("kling-video-o1", "std-audio", Decimal("0.9")),
            ("kling-video-o1", "pro", Decimal("0.8")),
            ("kling-video-o1", "pro-audio", Decimal("1.2")),
        ]
        special["models"]["kling-video"] = {
            "type": "kling_video",
            "credit_unit_price": unit,
            "billing_enabled": True,
            "default_duration": 5,
            "min_duration": 5,
            "max_duration": 10,
            "default_value": "kling-v1",
            "multipliers": {f"{version}|{mode}": float(multiplier) for version, mode, multiplier in kling_rows},
            "display": {
                "title": "分组价格",
                "description": "按版本、模式和时长计费",
                "unit": "秒",
                "credit_unit_price": unit,
                "billing_enabled": True,
                "columns": [
                    {"key": "version", "title": "版本"},
                    {"key": "mode", "title": "模式"},
                    {"key": "price", "title": "每秒价格"},
                ],
                "rows": [
                    {"version": version, "mode": mode, "multiplier": float(multiplier), "unit": "秒"}
                    for version, mode, multiplier in kling_rows
                ],
            },
        }

    if "pixverse-video" in source_names:
        special["models"]["pixverse-video"] = direct_display_only_rule(
            row_by_name["pixverse-video"],
            credit_unit_price,
            "当前项目没有完整 PixVerse 任务适配器，暂只展示，不启用真实特殊扣费",
        )

    for model_name, rule in list(special["models"].items()):
        display = rule.get("display") or {}
        if not display.get("sections"):
            section = {
                "title": display.get("title") or "分组价格",
                "description": display.get("description") or "",
                "unit": display.get("unit") or "次",
                "columns": display.get("columns") or [],
                "rows": display.get("rows") or [],
                "credit_unit_price": display.get("credit_unit_price") or rule.get("credit_unit_price") or unit,
            }
            display["sections"] = [section]
        min_price = min_price_from_sections(display["sections"])
        display["min_price"] = float(min_price)
        display["min_price_unit"] = display.get("unit") or "次"
        display["min_price_text"] = f"{price_text(min_price)} 起" if min_price > 0 else ""
        display["billing_enabled"] = bool(rule.get("billing_enabled"))
        rule["display"] = display
        special["models"][model_name] = rule

    display_only_reason = "cank 中存在特殊展示/计费分支，但当前项目无法安全从请求中推导完整规格，先仅展示不启用特殊扣费"
    for row in rows:
        model_name = row.get("model_name")
        if not model_name or model_name in special["models"] or not looks_special(str(model_name)):
            continue
        special["models"][model_name] = direct_display_only_rule(row, credit_unit_price, display_only_reason)

    return special


def build_option_maps(models: list[CatalogModel], fzbl: dict[str, Any], credit_unit_price: Decimal) -> dict[str, dict[str, Any]]:
    option_maps: dict[str, dict[str, Any]] = {
        "ModelRatio": {},
        "ModelPrice": {},
        "CompletionRatio": {},
        "CacheRatio": {},
        "GroupRatio": {},
        "GroupGroupRatio": {},
        "SpecialModelPricing": build_special_pricing(fzbl.get("data") or [], credit_unit_price),
    }
    for model in models:
        if model.model_price > 0:
            option_maps["ModelPrice"][model.name] = float(model.model_price)
        elif model.model_ratio > 0:
            option_maps["ModelRatio"][model.name] = float(model.model_ratio)
        if model.completion_ratio > 0:
            option_maps["CompletionRatio"][model.name] = float(model.completion_ratio)
        if model.cache_ratio is not None and model.cache_ratio > 0:
            option_maps["CacheRatio"][model.name] = float(model.cache_ratio)

    for key, value in (fzbl.get("group_ratio") or {}).items():
        option_maps["GroupRatio"][key] = value
    for key, value in (fzbl.get("group_group_ratio") or {}).items():
        option_maps["GroupGroupRatio"][key] = value
    return option_maps


def channel_key(channel_type: int, group: str, param_override: dict[str, Any], model_mapping: dict[str, str]) -> tuple[str, int, str]:
    if param_override or model_mapping:
        raw = json.dumps({"p": param_override, "m": model_mapping}, sort_keys=True, ensure_ascii=False)
        digest = hashlib.sha1(raw.encode("utf-8")).hexdigest()[:16]
        return (f"{SPECIAL_TAG_PREFIX}-{channel_type}-{slug(group)}-{digest}", channel_type, group)
    return (f"{SYNC_TAG}-{channel_type}-{slug(group)}", channel_type, group)


def build_channels(models: list[CatalogModel], key: str, base_url: str) -> list[dict[str, Any]]:
    grouped: dict[tuple[str, int, str], dict[str, Any]] = {}
    for model in models:
        for group in model.groups:
            tag, channel_type, group_name = channel_key(model.channel_type, group, model.param_override, model.model_mapping)
            key_tuple = (tag, channel_type, group_name)
            if key_tuple not in grouped:
                grouped[key_tuple] = {
                    "name": tag,
                    "tag": tag,
                    "type": channel_type,
                    "key": key,
                    "base_url": base_url,
                    "group": group_name,
                    "models": [],
                    "model_mapping": {},
                    "param_override": model.param_override,
                    "priority": 0,
                    "weight": 0,
                }
            item = grouped[key_tuple]
            item["models"].append(model.name)
            item["model_mapping"].update(model.model_mapping)
    channels = list(grouped.values())
    for channel in channels:
        channel["models"] = ",".join(unique_ordered(channel["models"]))
        channel["model_mapping"] = json.dumps(channel["model_mapping"], ensure_ascii=False, separators=(",", ":"))
        channel["param_override"] = json.dumps(channel["param_override"], ensure_ascii=False, separators=(",", ":")) if channel["param_override"] else ""
    return channels


def build_sql(
    vendors: list[dict[str, Any]],
    models: list[CatalogModel],
    cleanup_virtuals: list[CatalogModel],
    channels: list[dict[str, Any]],
    option_maps: dict[str, dict[str, Any]],
) -> str:
    now = int(time.time())
    lines = [
        "-- Generated by scripts/import_fzbl_catalog.py",
        "BEGIN;",
        "CREATE TEMP TABLE IF NOT EXISTS _fzbl_channel_ids(id integer) ON COMMIT DROP;",
        "TRUNCATE _fzbl_channel_ids;",
        "INSERT INTO _fzbl_channel_ids SELECT id FROM channels WHERE tag LIKE 'fzbl-sync%' OR tag LIKE 'fzbl-special%';",
        "DELETE FROM abilities WHERE channel_id IN (SELECT id FROM _fzbl_channel_ids);",
        "DELETE FROM channels WHERE id IN (SELECT id FROM _fzbl_channel_ids);",
    ]
    cleanup_names = unique_ordered([model.name for model in cleanup_virtuals])
    if cleanup_names:
        cleanup_sql_array = "ARRAY[" + ",".join(sql_str(name) for name in cleanup_names) + "]"
        lines.append(f"UPDATE models SET status=0, updated_time={now} WHERE model_name = ANY({cleanup_sql_array});")
        for option_key in ("ModelPrice", "ModelRatio", "CompletionRatio", "CacheRatio"):
            lines.append(
                f"UPDATE options SET value = (value::jsonb - {cleanup_sql_array})::text "
                f"WHERE key = {sql_str(option_key)} AND value IS NOT NULL AND value <> '';"
            )

    for vendor in vendors:
        vendor_id = int(vendor.get("id") or 0)
        if vendor_id <= 0:
            continue
        lines.append(
            "INSERT INTO vendors (id,name,description,icon,status,created_time,updated_time) VALUES "
            f"({vendor_id},{sql_str(vendor.get('name') or '')},{sql_str(vendor.get('description') or '')},"
            f"{sql_str(vendor.get('icon') or '')},1,{now},{now}) "
            "ON CONFLICT (id) DO UPDATE SET "
            "name=EXCLUDED.name, description=EXCLUDED.description, icon=EXCLUDED.icon, status=1, updated_time=EXCLUDED.updated_time;"
        )

    for model in models:
        lines.append(
            "UPDATE models SET "
            f"description={sql_str(model.description)}, icon='', tags={sql_str(model.tags)}, vendor_id={model.vendor_id}, "
            f"endpoints={sql_json(model.endpoint_map)}, status=1, sync_official=0, updated_time={now}, name_rule=0 "
            f"WHERE model_name={sql_str(model.name)} AND deleted_at IS NULL;"
        )
        lines.append(
            "INSERT INTO models (model_name,description,icon,tags,vendor_id,endpoints,status,sync_official,created_time,updated_time,name_rule) "
            "SELECT "
            f"{sql_str(model.name)},{sql_str(model.description)},'',{sql_str(model.tags)},{model.vendor_id},"
            f"{sql_json(model.endpoint_map)},1,0,{now},{now},0 "
            f"WHERE NOT EXISTS (SELECT 1 FROM models WHERE model_name={sql_str(model.name)} AND deleted_at IS NULL);"
        )

    for channel in channels:
        lines.append(
            "INSERT INTO channels (type,key,status,name,base_url,models,\"group\",model_mapping,priority,weight,tag,param_override,created_time) VALUES "
            f"({channel['type']},{sql_str(channel['key'])},1,{sql_str(channel['name'])},{sql_str(channel['base_url'])},"
            f"{sql_str(channel['models'])},{sql_str(channel['group'])},{sql_str(channel['model_mapping'])},"
            f"{channel['priority']},{channel['weight']},{sql_str(channel['tag'])},{sql_str(channel['param_override'])},{now}) "
            "ON CONFLICT DO NOTHING;"
        )
        lines.append(
            "INSERT INTO abilities (\"group\",model,channel_id,enabled,priority,weight,tag) "
            f"SELECT g.group_name, m.model_name, c.id, true, c.priority, c.weight, c.tag "
            f"FROM channels c, regexp_split_to_table({sql_str(channel['models'])}, ',') AS m(model_name), "
            f"regexp_split_to_table({sql_str(channel['group'])}, ',') AS g(group_name) "
            f"WHERE c.tag = {sql_str(channel['tag'])} "
            "ON CONFLICT (\"group\",model,channel_id) DO UPDATE SET enabled=true, priority=EXCLUDED.priority, weight=EXCLUDED.weight, tag=EXCLUDED.tag;"
        )

    for key, value in option_maps.items():
        lines.append(
            "INSERT INTO options (key,value) VALUES "
            f"({sql_str(key)},{sql_json(value)}) "
            "ON CONFLICT (key) DO UPDATE SET "
            "value = ((CASE WHEN options.value IS NULL OR options.value = '' THEN '{}' ELSE options.value END)::jsonb || EXCLUDED.value::jsonb)::text;"
        )

    lines.append("COMMIT;")
    lines.append("")
    return "\n".join(lines)


def write_report(out_dir: Path, report: dict[str, Any], sql: str) -> tuple[Path, Path]:
    out_dir.mkdir(parents=True, exist_ok=True)
    report_path = out_dir / "fzbl-import-report.json"
    sql_path = out_dir / "fzbl-import.sql"
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    sql_path.write_text(sql, encoding="utf-8")
    return report_path, sql_path


def apply_sql(sql_path: Path, compose_project: Path, postgres_service: str, db_user: str, db_name: str) -> None:
    cmd = [
        "docker",
        "compose",
        "exec",
        "-T",
        postgres_service,
        "psql",
        "-U",
        db_user,
        "-d",
        db_name,
    ]
    with sql_path.open("rb") as handle:
        subprocess.run(cmd, cwd=str(compose_project), stdin=handle, check=True)


def apply_sql_remote(
    sql_path: Path,
    remote: str,
    remote_project: str,
    postgres_service: str,
    db_user: str,
    db_name: str,
    ssh_port: int,
) -> None:
    remote_cmd = (
        f"cd {sh_quote(remote_project)} && "
        f"docker compose exec -T {sh_quote(postgres_service)} "
        f"psql -U {sh_quote(db_user)} -d {sh_quote(db_name)}"
    )
    cmd = ["ssh", "-p", str(ssh_port), remote, remote_cmd]
    with sql_path.open("rb") as handle:
        subprocess.run(cmd, stdin=handle, check=True)


def sh_quote(value: str) -> str:
    return "'" + value.replace("'", "'\"'\"'") + "'"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Normalize fzbl/cank catalog data for new-api.")
    parser.add_argument("--fzbl", default="../fzbl.txt", help="Path to fzbl.txt")
    parser.add_argument("--cank", default="../cank.txt", help="Path to cank.txt, used for presence checks")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument(
        "--credit-unit-price",
        default="0.05",
        help="Unit price for cank credit multipliers. 0.05 matches the referenced pricing page.",
    )
    parser.add_argument(
        "--split-special",
        action="store_true",
        help="Expose special specs as virtual models. Default keeps original model names.",
    )
    parser.add_argument("--key", default=os.environ.get("FZBL_UPSTREAM_KEY", ""), help="Upstream API key, or FZBL_UPSTREAM_KEY")
    parser.add_argument("--out-dir", default="tmp/fzbl-import")
    parser.add_argument("--apply", action="store_true", help="Apply generated SQL through docker compose exec")
    parser.add_argument("--compose-project", default=".")
    parser.add_argument("--remote", default="", help="SSH target such as root@154.12.60.218")
    parser.add_argument("--remote-project", default="/opt/new-api", help="Remote compose project path")
    parser.add_argument("--ssh-port", default=22, type=int)
    parser.add_argument("--postgres-service", default="postgres")
    parser.add_argument("--db-user", default="root")
    parser.add_argument("--db-name", default="new-api")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    fzbl_path = Path(args.fzbl)
    cank_path = Path(args.cank)
    if not fzbl_path.exists():
        print(f"fzbl file not found: {fzbl_path}", file=sys.stderr)
        return 2
    if not cank_path.exists():
        print(f"cank file not found: {cank_path}", file=sys.stderr)
        return 2
    if args.apply and not args.key:
        print("missing --key or FZBL_UPSTREAM_KEY for --apply", file=sys.stderr)
        return 2
    if args.remote and not args.apply:
        print("--remote requires --apply", file=sys.stderr)
        return 2

    fzbl = json.loads(fzbl_path.read_text(encoding="utf-8"))
    rows = fzbl.get("data") or []
    credit_unit_price = dec(args.credit_unit_price)
    endpoint_catalog = fzbl.get("supported_endpoint") or {}
    models, cleanup_virtuals, skipped = expand_models(rows, endpoint_catalog, credit_unit_price, args.split_special)
    option_maps = build_option_maps(models, fzbl, credit_unit_price)
    special_models = (option_maps.get("SpecialModelPricing") or {}).get("models", {})
    skipped = [item for item in skipped if item.get("model_name") not in special_models]
    key_for_sql = args.key or "__DRY_RUN_KEY__"
    channels = build_channels(models, key_for_sql, args.base_url)
    sql = build_sql(fzbl.get("vendors") or [], models, cleanup_virtuals, channels, option_maps)

    virtual_count = sum(1 for model in models if model.is_virtual)
    report = {
        "source_models": len(rows),
        "normalized_models": len(models),
        "virtual_models": virtual_count,
        "cleanup_virtual_models": len(cleanup_virtuals),
        "base_models": len(models) - virtual_count,
        "channels": len(channels),
        "special_pricing_models": sorted(special_models.keys()),
        "billing_enabled_special_models": sorted(
            name for name, rule in special_models.items() if rule.get("billing_enabled")
        ),
        "display_only_special_models": sorted(
            name for name, rule in special_models.items() if not rule.get("billing_enabled")
        ),
        "special_min_prices": {
            name: (rule.get("display") or {}).get("min_price")
            for name, rule in sorted(special_models.items())
        },
        "groups": sorted({group for model in models for group in model.groups}),
        "option_counts": {key: len(value) for key, value in option_maps.items()},
        "credit_unit_price": float(credit_unit_price),
        "skipped_special_models": skipped,
        "sample_virtual_models": [
            {
                "name": model.name,
                "upstream": model.upstream_name,
                "price": float(model.model_price),
                "groups": model.groups,
                "rule": model.source_rule,
            }
            for model in models
            if model.is_virtual
        ][:30],
    }
    report_path, sql_path = write_report(Path(args.out_dir), report, sql)

    print(f"report: {report_path}")
    print(f"sql: {sql_path}")
    print(json.dumps(report, ensure_ascii=False, indent=2))

    if args.apply:
        if args.remote:
            apply_sql_remote(
                sql_path,
                args.remote,
                args.remote_project,
                args.postgres_service,
                args.db_user,
                args.db_name,
                args.ssh_port,
            )
        else:
            apply_sql(sql_path, Path(args.compose_project), args.postgres_service, args.db_user, args.db_name)
        print("apply complete")
    else:
        print("dry-run only; add --apply to import")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
