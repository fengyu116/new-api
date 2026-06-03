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


def sections_have_price(sections: list[dict[str, Any]]) -> bool:
    for section in sections:
        for row in section.get("rows") or []:
            if (
                row.get("price") is not None
                or row.get("first_second_price") is not None
                or row.get("multiplier") is not None
            ):
                return True
    return False


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
    min_price_text = f"{price_text(min_price)} 起" if sections_have_price(sections) else ""
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
        "min_price_text": min_price_text,
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


CANK_SPECIAL_MODELS = {
    "MiniMax-Hailuo-02",
    "MiniMax-Hailuo-2.3",
    "MiniMax-Hailuo-2.3-Fast",
    "aigc-template-effect-vidu",
    "aigc-video-hailuo",
    "aigc-video-kling",
    "aigc-video-vidu",
    "alibailian-video",
    "audio1.0",
    "doubao-seedance-2-0-260128",
    "doubao-seedance-2-0-fast-260128",
    "gemini-3-pro-image-preview",
    "gemini-3.1-flash-image-preview",
    "jimeng-videos",
    "kling-advanced-lip-sync",
    "kling-audio",
    "kling-avatar-image2video",
    "kling-effects",
    "kling-image",
    "kling-image-recognize",
    "kling-kolors-virtual-try-on",
    "kling-motion-control",
    "kling-multi-elements",
    "kling-omni-image",
    "kling-omni-video",
    "kling-video",
    "kling-video-extend",
    "pixverse-image-template",
    "pixverse-lipsync",
    "pixverse-mask-selection",
    "pixverse-mimic",
    "pixverse-modify",
    "pixverse-multi-transition",
    "pixverse-restyle",
    "pixverse-sound-effect",
    "pixverse-swap",
    "pixverse-upload",
    "pixverse-video",
    "sora-2",
    "sora-2-pro",
    "suno_music_open",
    "vidu-tts",
    "vidu2.0",
    "viduq1",
    "viduq1-classic",
    "viduq2",
    "viduq2-pro",
    "viduq2-turbo",
    "viduq3",
    "viduq3-mix",
    "viduq3-pro",
    "viduq3-turbo",
    "wan2.5-i2v-preview",
    "wan2.6-i2v",
    "wan2.6-i2v-flash",
}


def credit_money(multiplier: Decimal | float | int, credit_unit_price: Decimal) -> Decimal:
    return dec(multiplier) * credit_unit_price


def fixed_entry_rule(
    title: str,
    description: str,
    key_fields: list[str],
    rows: list[dict[str, Any]],
    credit_unit_price: Decimal,
    columns: list[dict[str, str]] | None = None,
    default_duration: int = 0,
    min_duration: int = 0,
    max_duration: int = 0,
    default_value: str = "",
    unit: str = "次",
) -> dict[str, Any]:
    if columns is None:
        columns = [
            {"key": "description", "title": "规格"},
            {"key": "price", "title": "价格"},
        ]
    entries = []
    display_rows = []
    for row in rows:
        key = row["key"]
        price = credit_money(row["credits"], credit_unit_price)
        entry: dict[str, Any] = {"key": key, "price": float(price), "unit": unit}
        if "first_credits" in row or "next_credits" in row:
            entry.pop("price", None)
            entry["first_second_price"] = float(credit_money(row.get("first_credits", 0), credit_unit_price))
            if row.get("second_credits") is not None:
                entry["second_second_price"] = float(credit_money(row.get("second_credits", 0), credit_unit_price))
            entry["next_second_price"] = float(credit_money(row.get("next_credits", 0), credit_unit_price))
        if row.get("addons"):
            entry["addons"] = {name: float(credit_money(value, credit_unit_price)) for name, value in row["addons"].items()}
        entries.append(entry)

        display = {k: v for k, v in row.items() if k not in {"key", "credits", "first_credits", "second_credits", "next_credits", "addons"}}
        if "first_credits" in row or "next_credits" in row:
            first = credit_money(row.get("first_credits", 0), credit_unit_price)
            second = credit_money(row.get("second_credits", 0), credit_unit_price) if row.get("second_credits") is not None else None
            next_second = credit_money(row.get("next_credits", 0), credit_unit_price)
            display["first_second_price"] = float(first)
            if second is not None:
                display["second_second_price"] = float(second)
            display["next_second_price"] = float(next_second)
            if second is not None:
                display["price_text"] = f"第1秒{price_text(first)}，第2秒{price_text(second)}，第3秒开始每秒+{price_text(next_second)}"
            else:
                display["price_text"] = f"第1秒{price_text(first)}，后续每秒+{price_text(next_second)}"
        else:
            display["price"] = float(price)
            display["price_text"] = price_text(price)
        display["unit"] = unit
        display_rows.append(display)

    sections = [
        {
            "title": title,
            "description": description,
            "unit": unit,
            "credit_unit_price": float(credit_unit_price),
            "columns": columns,
            "rows": display_rows,
        }
    ]
    return {
        "type": "entry_fields",
        "credit_unit_price": float(credit_unit_price),
        "billing_enabled": True,
        "key_fields": key_fields,
        "default_duration": default_duration,
        "min_duration": min_duration,
        "max_duration": max_duration,
        "default_value": default_value,
        "entries": entries,
        "display": display_config(title, description, unit, credit_unit_price, True, sections),
    }


def per_second_rule(
    title: str,
    description: str,
    key_fields: list[str],
    rows: list[dict[str, Any]],
    credit_unit_price: Decimal,
    columns: list[dict[str, str]],
    default_duration: int,
    min_duration: int,
    max_duration: int,
    default_value: str = "",
) -> dict[str, Any]:
    return fixed_entry_rule(
        title,
        description,
        key_fields,
        [{**row, "first_credits": row["credits"], "next_credits": row["credits"]} for row in rows],
        credit_unit_price,
        columns=columns,
        default_duration=default_duration,
        min_duration=min_duration,
        max_duration=max_duration,
        default_value=default_value,
        unit="秒",
    )


def vidu_one_price_rule(model_label: str, rows: list[dict[str, Any]], credit_unit_price: Decimal) -> dict[str, Any]:
    return fixed_entry_rule(
        "分组价格",
        "按 cank 固定规格计费",
        ["ability", "resolution", "duration"],
        rows,
        credit_unit_price,
        columns=[
            {"key": "ability", "title": "能力"},
            {"key": "resolution", "title": "清晰度"},
            {"key": "duration", "title": "时长"},
            {"key": "price", "title": "价格"},
        ],
        default_duration=5,
        default_value="1080p",
        unit="次",
    )


def vidu_q2_variant_rule(model_label: str, table: list[tuple[str, str, Decimal, Decimal | None, Decimal]], credit_unit_price: Decimal) -> dict[str, Any]:
    rows = []
    for resolution, label, first, second, next_second in table:
        row = {
            "key": f"reference|{resolution}",
            "ability": "图生&首尾帧",
            "model": model_label,
            "resolution": resolution.upper(),
            "credits": 0,
            "first_credits": first,
            "second_credits": second,
            "next_credits": next_second,
            "addons": {"with_audio": 15, "recommend_prompt": 10, "prompt_optimizer": 10, "enhance_prompt": 10},
        }
        price = f"第1秒{price_text(credit_money(first, credit_unit_price))}"
        if second is not None:
            price += f"，第2秒{price_text(credit_money(second, credit_unit_price))}"
            row["description"] = label
        price += f"，后续每秒+{price_text(credit_money(next_second, credit_unit_price))}"
        row["price_text"] = price
        rows.append(row)
    rule = fixed_entry_rule(
        "分组价格",
        "图生音视频直出额外 💰0.7500；启用推荐提示词额外 💰0.5000",
        ["ability", "resolution"],
        rows,
        credit_unit_price,
        columns=[
            {"key": "ability", "title": "能力"},
            {"key": "model", "title": "模型"},
            {"key": "resolution", "title": "分辨率"},
            {"key": "price", "title": "定价"},
        ],
        default_duration=5,
        min_duration=1,
        max_duration=16,
        default_value="1080p",
        unit="次",
    )
    for display_row, source in zip(rule["display"]["sections"][0]["rows"], rows):
        if source.get("price_text"):
            display_row["price_text"] = source["price_text"]
    return rule


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

    if "viduq2-pro" in source_names:
        special["models"]["viduq2-pro"] = vidu_q2_variant_rule(
            "Q2-pro",
            [
                ("540p", "540P", Decimal("8"), Decimal("10"), Decimal("5")),
                ("720p", "720P", Decimal("15"), None, Decimal("10")),
                ("1080p", "1080P", Decimal("55"), None, Decimal("15")),
            ],
            credit_unit_price,
        )
    if "viduq2-turbo" in source_names:
        special["models"]["viduq2-turbo"] = vidu_q2_variant_rule(
            "Q2-turbo",
            [
                ("540p", "540P", Decimal("6"), None, Decimal("2")),
                ("720p", "720P", Decimal("8"), Decimal("10"), Decimal("10")),
                ("1080p", "1080P", Decimal("35"), None, Decimal("10")),
            ],
            credit_unit_price,
        )
    if "viduq1" in source_names:
        special["models"]["viduq1"] = vidu_one_price_rule(
            "Q1",
            [
                {"key": f"{ability}|1080p|5", "ability": label, "resolution": "1080P", "duration": "5S", "credits": 80}
                for ability, label in [
                    ("reference", "参考生视频"),
                    ("image", "图生视频"),
                    ("text", "文生视频"),
                ]
            ]
            + [{"key": "reference|1080p|5", "ability": "首尾帧", "resolution": "1080P", "duration": "5S", "credits": 80}]
            + [{"key": "reference|1080p|0", "ability": "参考生图", "resolution": "1080P", "duration": "-", "credits": 20}],
            credit_unit_price,
        )
    if "viduq1-classic" in source_names:
        special["models"]["viduq1-classic"] = vidu_one_price_rule(
            "Q1 Classic",
            [
                {"key": "image|1080p|5", "ability": "图生视频", "resolution": "1080P", "duration": "5S", "credits": 80},
                {"key": "reference|1080p|5", "ability": "首尾帧", "resolution": "1080P", "duration": "5S", "credits": 80},
            ],
            credit_unit_price,
        )
    if "vidu2.0" in source_names:
        rows = []
        for ability, label in [("image", "图生视频"), ("reference", "首尾帧")]:
            for resolution, duration, credits in [
                ("360p", 4, 20),
                ("720p", 4, 40),
                ("720p", 8, 100),
                ("1080p", 4, 100),
            ]:
                rows.append({"key": f"{ability}|{resolution}|{duration}", "ability": label, "resolution": resolution.upper(), "duration": f"{duration}s", "credits": credits})
        rows.extend(
            [
                {"key": "reference|360p|4", "ability": "参考生视频", "resolution": "360P", "duration": "4s", "credits": 80},
                {"key": "reference|720p|4", "ability": "参考生视频", "resolution": "720P", "duration": "4s", "credits": 80},
            ]
        )
        special["models"]["vidu2.0"] = vidu_one_price_rule("Vidu 2.0", rows, credit_unit_price)
    if "audio1.0" in source_names:
        special["models"]["audio1.0"] = fixed_entry_rule(
            "音频生成",
            "按时长档位计费，duration 小于等于 5/10 秒匹配对应档位",
            ["duration_range"],
            [
                {"key": "5", "ability": "文生音频 / 可控文生音效", "duration": "小于5秒", "credits": 10},
                {"key": "10", "ability": "文生音频 / 可控文生音效", "duration": "小于10秒", "credits": 20},
            ],
            credit_unit_price,
            columns=[
                {"key": "ability", "title": "能力"},
                {"key": "duration", "title": "时长"},
                {"key": "price", "title": "价格"},
            ],
            default_duration=5,
            min_duration=1,
            max_duration=10,
        )
    if "vidu-tts" in source_names:
        special["models"]["vidu-tts"] = fixed_entry_rule(
            "语音合成",
            "每 500 字符固定计费",
            ["feature"],
            [{"key": "tts", "description": "语音合成 / 500 字符", "credits": 10}],
            credit_unit_price,
            default_value="tts",
        )

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

    if "kling-image" in source_names:
        special["models"]["kling-image"] = fixed_entry_rule(
            "单张价格",
            "按张计费，参数 n 范围 1-9",
            ["version", "generation_type", "resolution"],
            [
                {"key": "kling-v1|text|1k", "version": "kling-v1", "generation_type": "文生图", "resolution": "1K", "credits": 1},
                {"key": "kling-v1|image|1k", "version": "kling-v1", "generation_type": "图生图", "resolution": "1K", "credits": 1},
                {"key": "kling-v1-5|text|1k", "version": "kling-v1-5", "generation_type": "文生图", "resolution": "1K", "credits": 4},
                {"key": "kling-v1-5|image|1k", "version": "kling-v1-5", "generation_type": "图生图", "resolution": "1K", "credits": 8},
                {"key": "kling-v2|text|1k", "version": "kling-v2", "generation_type": "文生图", "resolution": "1K/2K", "credits": 4},
                {"key": "kling-v2|text|2k", "version": "kling-v2", "generation_type": "文生图", "resolution": "1K/2K", "credits": 4},
                {"key": "kling-v2|image|1k", "version": "kling-v2", "generation_type": "图生图", "resolution": "1K", "credits": 8},
                {"key": "kling-v2|multi_image|1k", "version": "kling-v2", "generation_type": "多图生图", "resolution": "1K", "credits": 16},
                {"key": "kling-v2-new|image|1k", "version": "kling-v2-new", "generation_type": "图生图", "resolution": "1K", "credits": 8},
                {"key": "kling-v2-1|text|1k", "version": "kling-v2-1", "generation_type": "文生图", "resolution": "1K", "credits": 4},
                {"key": "kling-v2-1|multi_image|1k", "version": "kling-v2-1", "generation_type": "多图生图", "resolution": "1K", "credits": 16},
                {"key": "kling-v3|text|1k", "version": "kling-v3", "generation_type": "文生图/图生图", "resolution": "1K/2K", "credits": 8},
                {"key": "kling-v3|image|1k", "version": "kling-v3", "generation_type": "文生图/图生图", "resolution": "1K/2K", "credits": 8},
                {"key": "kling-v3|text|2k", "version": "kling-v3", "generation_type": "文生图/图生图", "resolution": "1K/2K", "credits": 8},
                {"key": "kling-v3|image|2k", "version": "kling-v3", "generation_type": "文生图/图生图", "resolution": "1K/2K", "credits": 8},
                {"key": "-|outpaint|1k", "version": "-", "generation_type": "扩图", "resolution": "1K", "credits": 8},
            ],
            credit_unit_price,
            columns=[
                {"key": "version", "title": "模型版本"},
                {"key": "generation_type", "title": "生成类型"},
                {"key": "resolution", "title": "分辨率"},
                {"key": "price", "title": "单张价格"},
            ],
            default_value="kling-v1",
        )
    if "kling-omni-image" in source_names:
        special["models"]["kling-omni-image"] = fixed_entry_rule(
            "单张价格",
            "按张计费，参数 n 范围 1-9",
            ["version", "resolution"],
            [
                {"key": "kling-image-o1|1k", "version": "kling-image-o1", "generation_type": "文生图/图生图", "resolution": "1K/2K", "credits": 1},
                {"key": "kling-image-o1|2k", "version": "kling-image-o1", "generation_type": "文生图/图生图", "resolution": "1K/2K", "credits": 1},
                {"key": "kling-v3-omni|1k", "version": "kling-v3-omni", "generation_type": "文生图/图生图", "resolution": "1K/2K", "credits": 1},
                {"key": "kling-v3-omni|2k", "version": "kling-v3-omni", "generation_type": "文生图/图生图", "resolution": "1K/2K", "credits": 1},
                {"key": "kling-v3-omni|4k", "version": "kling-v3-omni", "generation_type": "文生图/图生图", "resolution": "4K", "credits": 2},
            ],
            credit_unit_price,
            columns=[
                {"key": "version", "title": "模型版本"},
                {"key": "generation_type", "title": "生成类型"},
                {"key": "resolution", "title": "分辨率"},
                {"key": "price", "title": "单张价格"},
            ],
            default_value="kling-image-o1",
        )
    if "kling-omni-video" in source_names:
        rows = [
            ("kling-video-o1", "std", "no_video", "no_audio", Decimal("0.6")),
            ("kling-video-o1", "std", "video", "no_audio", Decimal("0.9")),
            ("kling-video-o1", "pro", "no_video", "no_audio", Decimal("0.8")),
            ("kling-video-o1", "pro", "video", "no_audio", Decimal("1.2")),
            ("kling-v3-omni", "std", "no_video", "no_audio", Decimal("0.6")),
            ("kling-v3-omni", "std", "no_video", "audio", Decimal("0.8")),
            ("kling-v3-omni", "std", "video", "no_audio", Decimal("0.9")),
            ("kling-v3-omni", "pro", "no_video", "no_audio", Decimal("0.8")),
            ("kling-v3-omni", "pro", "no_video", "audio", Decimal("1")),
            ("kling-v3-omni", "pro", "video", "no_audio", Decimal("1.2")),
        ]
        special["models"]["kling-omni-video"] = per_second_rule(
            "分组价格",
            "按版本、模式、参考视频和音频按秒计费",
            ["version", "mode", "has_video", "with_audio"],
            [
                {"key": f"{version}|{mode}|{has_video}|{audio}", "version": version, "mode": mode, "has_video": has_video, "has_audio": audio, "credits": credits}
                for version, mode, has_video, audio, credits in rows
            ],
            credit_unit_price,
            columns=[
                {"key": "version", "title": "模型版本"},
                {"key": "mode", "title": "模式"},
                {"key": "has_video", "title": "是否有参考视频"},
                {"key": "has_audio", "title": "音频"},
                {"key": "price", "title": "每秒价格"},
            ],
            default_duration=5,
            min_duration=3,
            max_duration=15,
            default_value="kling-video-o1",
        )
    if "kling-effects" in source_names:
        effect_rows = [
            ("ultra_high", "超高价特效", 9, "如 french_elegance, flash_drive 等"),
            ("high", "高价特效", 7, "如 bullet_time, sedan_chair_dance 等"),
            ("mid_high", "中高价特效", 5, "如 daoma_dance, expression_challenge 等"),
            ("mid", "中价特效", Decimal("3.5"), "如 birthday_star, happy_birthday 等"),
            ("mid_low", "中低价特效", 2, "如 running_man, angel_wing 等"),
            ("low", "低价特效", Decimal("1.5"), "如 hug_pro, kiss_pro 等"),
            ("base", "超低价特效", 1, "如 a_list_look, boss_coming 等"),
        ]
        special["models"]["kling-effects"] = fixed_entry_rule(
            "特效价格",
            "按 effect_scene 固定价格计费；完整 effect_scene 映射需由请求传入对应档位 key",
            ["effect_scene"],
            [{"key": key, "effect_type": label, "description": note, "credits": credits} for key, label, credits, note in effect_rows],
            credit_unit_price,
            columns=[
                {"key": "effect_type", "title": "特效类型"},
                {"key": "price", "title": "价格示例"},
                {"key": "description", "title": "说明"},
            ],
            default_value="base",
        )
    if "kling-audio" in source_names:
        special["models"]["kling-audio"] = fixed_entry_rule(
            "音频",
            "按功能固定计费",
            ["feature"],
            [
                {"key": "text_sound", "feature": "文生音效", "credits": 5},
                {"key": "video_sound", "feature": "视频生音效", "credits": 5},
                {"key": "tts", "feature": "语音合成", "credits": 1},
            ],
            credit_unit_price,
            columns=[
                {"key": "feature", "title": "模型"},
                {"key": "price", "title": "价格"},
            ],
            default_value="text_sound",
        )
    if "kling-multi-elements" in source_names:
        special["models"]["kling-multi-elements"] = fixed_entry_rule(
            "多元素视频",
            "按版本、模式、时长计费",
            ["version", "mode", "duration"],
            [
                {"key": "kling-v1-6|std|5", "version": "kling-v1-6", "mode": "std", "duration": "5s", "credits": 3},
                {"key": "kling-v1-6|std|10", "version": "kling-v1-6", "mode": "std", "duration": "10s", "credits": 6},
                {"key": "kling-v1-6|pro|5", "version": "kling-v1-6", "mode": "pro", "duration": "5s", "credits": 5},
                {"key": "kling-v1-6|pro|10", "version": "kling-v1-6", "mode": "pro", "duration": "10s", "credits": 10},
            ],
            credit_unit_price,
            columns=[
                {"key": "version", "title": "模型版本"},
                {"key": "mode", "title": "模式"},
                {"key": "duration", "title": "时长"},
                {"key": "price", "title": "价格"},
            ],
            default_duration=5,
            min_duration=5,
            max_duration=10,
            default_value="kling-v1-6",
        )
    if "kling-image-recognize" in source_names:
        special["models"]["kling-image-recognize"] = fixed_entry_rule(
            "图像识别",
            "每次固定计费",
            ["feature"],
            [{"key": "recognize", "description": "图像识别", "credits": 1}],
            credit_unit_price,
            default_value="recognize",
        )
    if "kling-video-extend" in source_names:
        rows = []
        for version, std, pro in [("kling-v1", 1, Decimal("3.5")), ("kling-v1-5", 2, Decimal("3.5")), ("kling-v1-6", 2, Decimal("3.5"))]:
            rows.append({"key": f"{version}|std", "version": version, "mode": "std", "duration": "4~5s", "credits": std})
            rows.append({"key": f"{version}|pro", "version": version, "mode": "pro", "duration": "4~5s", "credits": pro})
        special["models"]["kling-video-extend"] = fixed_entry_rule(
            "视频延长",
            "按版本和模式固定计费",
            ["version", "mode"],
            rows,
            credit_unit_price,
            columns=[
                {"key": "version", "title": "模型版本"},
                {"key": "mode", "title": "模式"},
                {"key": "duration", "title": "时长"},
                {"key": "price", "title": "价格"},
            ],
            default_value="kling-v1",
        )
    if "kling-avatar-image2video" in source_names:
        special["models"]["kling-avatar-image2video"] = per_second_rule(
            "数字人图生视频",
            "按模式和秒数计费",
            ["mode"],
            [
                {"key": "std", "mode": "std", "credits": 1},
                {"key": "pro", "mode": "pro", "credits": 2},
            ],
            credit_unit_price,
            columns=[
                {"key": "mode", "title": "模式"},
                {"key": "price", "title": "价格（按秒计费）"},
            ],
            default_duration=5,
            min_duration=1,
            max_duration=60,
            default_value="std",
        )
    if "kling-advanced-lip-sync" in source_names:
        special["models"]["kling-advanced-lip-sync"] = fixed_entry_rule(
            "高级对口型",
            "人脸识别按次；对口型按每 5 秒档位计费",
            ["feature"],
            [
                {"key": "face_detect", "feature": "人脸识别", "credits": Decimal("0.1")},
                {"key": "lip_sync", "feature": "对口型 / 每5秒", "credits": 1},
            ],
            credit_unit_price,
            columns=[
                {"key": "feature", "title": "类型"},
                {"key": "price", "title": "价格"},
            ],
            default_value="lip_sync",
        )
    if "kling-motion-control" in source_names:
        special["models"]["kling-motion-control"] = per_second_rule(
            "动作控制",
            "按模式按秒计费",
            ["mode"],
            [
                {"key": "std", "mode": "std/720P", "credits": Decimal("50") / Decimal("30")},
                {"key": "pro", "mode": "pro/1080P", "credits": Decimal("80") / Decimal("30")},
            ],
            credit_unit_price,
            columns=[
                {"key": "mode", "title": "模式"},
                {"key": "price", "title": "每秒价格"},
            ],
            default_duration=5,
            min_duration=5,
            max_duration=10,
            default_value="std",
        )

    if "pixverse-video" in source_names:
        rows = []
        for version, table in {
            "c1": [("360p", 6, 8), ("540p", 8, 10), ("720p", 10, 13), ("1080p", 19, 24)],
            "v6": [("360p", 5, 7), ("540p", 7, 9), ("720p", 9, 12), ("1080p", 18, 23)],
        }.items():
            for resolution, no_audio, audio in table:
                rows.append({"key": f"{version}|{resolution}|no_audio", "version": version.upper(), "resolution": resolution, "with_audio": "不带声音", "credits": no_audio})
                rows.append({"key": f"{version}|{resolution}|audio", "version": version.upper(), "resolution": resolution, "with_audio": "带声音", "credits": audio})
        special["models"]["pixverse-video"] = per_second_rule(
            "视频生成",
            "C1/V6 按秒计费；旧版本按次模板价在展示中保留，真实扣费优先使用 version/resolution/audio",
            ["version", "resolution", "with_audio"],
            rows,
            credit_unit_price,
            columns=[
                {"key": "version", "title": "版本"},
                {"key": "resolution", "title": "分辨率"},
                {"key": "with_audio", "title": "声音"},
                {"key": "price", "title": "每秒价格"},
            ],
            default_duration=5,
            min_duration=1,
            max_duration=30,
            default_value="c1",
        )
    for pix_model, title, credits in [
        ("pixverse-lipsync", "音频对口型", 4),
        ("pixverse-restyle", "视频重绘", 10),
        ("pixverse-sound-effect", "音效生成", 2),
    ]:
        if pix_model in source_names:
            special["models"][pix_model] = per_second_rule(
                title,
                "按 duration 秒数计费",
                ["feature"],
                [{"key": "default", "description": title, "credits": credits}],
                credit_unit_price,
                columns=[
                    {"key": "description", "title": "计费方式"},
                    {"key": "price", "title": "每秒价格"},
                ],
                default_duration=5,
                min_duration=1,
                max_duration=300,
                default_value="default",
            )
    if "pixverse-multi-transition" in source_names:
        base_360 = {1: 23, 2: 27, 3: 32, 4: 36, 5: 45, 6: 59, 7: 72, 8: 90, 9: 95, 10: 99, 11: 104, 12: 108, 13: 117, 14: 126, 15: 135, 16: 144, 17: 153, 18: 162, 19: 171, 20: 180, 21: 189, 22: 198, 23: 207, 24: 216, 25: 225, 26: 234, 27: 243, 28: 252, 29: 261, 30: 270}
        base_720 = {1: 30, 2: 36, 3: 42, 4: 48, 5: 60, 6: 78, 7: 96, 8: 120, 9: 126, 10: 132, 11: 138, 12: 144, 13: 156, 14: 168, 15: 180, 16: 192, 17: 204, 18: 216, 19: 228, 20: 240, 21: 252, 22: 264, 23: 276, 24: 288, 25: 300, 26: 312, 27: 324, 28: 336, 29: 348, 30: 360}
        rows = []
        for duration in range(1, 31):
            for resolution, credits in [("360p", base_360[duration]), ("540p", base_360[duration]), ("720p", base_720[duration]), ("1080p", base_720[duration] * 2)]:
                rows.append({"key": f"{resolution}|{duration}", "resolution": resolution.upper(), "duration": f"{duration}s", "credits": credits})
        special["models"]["pixverse-multi-transition"] = fixed_entry_rule(
            "多图转场",
            "按总时长与分辨率固定计费；总时长 = 每帧 duration 求和",
            ["resolution", "duration"],
            rows,
            credit_unit_price,
            columns=[
                {"key": "resolution", "title": "分辨率"},
                {"key": "duration", "title": "视频总时长"},
                {"key": "price", "title": "价格"},
            ],
            default_duration=5,
            min_duration=1,
            max_duration=30,
            default_value="720p",
        )
    for pix_model, title, table in [
        ("pixverse-swap", "换脸/替换", {"360p": 9, "540p": 9, "720p": 12}),
        ("pixverse-mimic", "动作模仿", {"360p": 9, "540p": 10, "720p": 12}),
        ("pixverse-modify", "视频修改", {"360p": 8, "540p": 10, "720p": 12}),
    ]:
        if pix_model in source_names:
            special["models"][pix_model] = per_second_rule(
                title,
                "按分辨率和秒数计费",
                ["resolution"],
                [{"key": resolution, "resolution": resolution.upper(), "credits": credits} for resolution, credits in table.items()],
                credit_unit_price,
                columns=[
                    {"key": "resolution", "title": "分辨率"},
                    {"key": "price", "title": "每秒价格"},
                ],
                default_duration=5,
                min_duration=1,
                max_duration=300,
                default_value="540p",
            )
    if "pixverse-mask-selection" in source_names:
        special["models"]["pixverse-mask-selection"] = fixed_entry_rule("Mask 抠图", "同步固定计费", ["feature"], [{"key": "mask", "description": "Mask 抠图", "credits": 2}], credit_unit_price, default_value="mask")
    if "pixverse-image-template" in source_names:
        special["models"]["pixverse-image-template"] = fixed_entry_rule("图片模板", "模板价格按 action/template_id 档位计费", ["feature"], [{"key": "image_template", "description": "图片模板基础价", "credits": 1}], credit_unit_price, default_value="image_template")

    for model_name, rows, default_resolution, max_duration in [
        (
            "wan2.5-i2v-preview",
            [("480p", Decimal("0.3")), ("720p", Decimal("0.6")), ("1080p", Decimal("1"))],
            "480p",
            10,
        ),
        (
            "wan2.6-i2v",
            [("720p", Decimal("0.6")), ("1080p", Decimal("1"))],
            "720p",
            15,
        ),
        (
            "wan2.6-i2v-flash",
            [("720p", Decimal("0.6")), ("1080p", Decimal("1"))],
            "720p",
            10,
        ),
    ]:
        if model_name in source_names:
            special["models"][model_name] = per_second_rule(
                "视频生成",
                "按分辨率和秒数计费",
                ["resolution"],
                [{"key": resolution, "resolution": resolution.upper(), "credits": credits} for resolution, credits in rows],
                credit_unit_price,
                columns=[
                    {"key": "resolution", "title": "分辨率"},
                    {"key": "price", "title": "每秒价格"},
                ],
                default_duration=5,
                min_duration=1,
                max_duration=max_duration,
                default_value=default_resolution,
            )
    for model_name, rows in [
        (
            "MiniMax-Hailuo-02",
            [
                ("768p", 6, 1),
                ("768p", 10, 2),
                ("1080p", 6, Decimal("1.75")),
                ("512p", 6, Decimal("0.3")),
                ("512p", 10, Decimal("0.5")),
            ],
        ),
        (
            "MiniMax-Hailuo-2.3",
            [
                ("768p", 6, 1),
                ("768p", 10, 2),
                ("1080p", 6, Decimal("1.75")),
            ],
        ),
    ]:
        if model_name in source_names:
            special["models"][model_name] = fixed_entry_rule(
                "视频生成",
                "按分辨率与时长固定计费",
                ["resolution", "duration"],
                [
                    {"key": f"{resolution}|{duration}", "version": model_name, "resolution": resolution.upper(), "duration": f"{duration}s", "credits": credits}
                    for resolution, duration, credits in rows
                ],
                credit_unit_price,
                columns=[
                    {"key": "version", "title": "模型版本"},
                    {"key": "resolution", "title": "分辨率"},
                    {"key": "duration", "title": "时长"},
                    {"key": "price", "title": "价格"},
                ],
                default_duration=6,
                min_duration=6,
                max_duration=10,
                default_value="768p",
            )
    if "gemini-3-pro-image-preview" in source_names:
        special["models"]["gemini-3-pro-image-preview"] = fixed_entry_rule(
            "图像生成",
            "按分辨率固定计费",
            ["resolution"],
            [
                {"key": "1k", "resolution": "1K", "credits": 1},
                {"key": "2k", "resolution": "2K", "credits": 1},
                {"key": "4k", "resolution": "4K", "credits": Decimal("1.79")},
            ],
            credit_unit_price,
            columns=[{"key": "resolution", "title": "分辨率"}, {"key": "price", "title": "价格"}],
            default_value="1k",
        )
    if "gemini-3.1-flash-image-preview" in source_names:
        special["models"]["gemini-3.1-flash-image-preview"] = fixed_entry_rule(
            "图像生成",
            "按分辨率固定计费",
            ["resolution"],
            [
                {"key": "512", "resolution": "512", "credits": Decimal("0.66696")},
                {"key": "1k", "resolution": "1K", "credits": 1},
                {"key": "2k", "resolution": "2K", "credits": 1},
                {"key": "4k", "resolution": "4K", "credits": Decimal("1.78571")},
            ],
            credit_unit_price,
            columns=[{"key": "resolution", "title": "分辨率"}, {"key": "price", "title": "价格"}],
            default_value="1k",
        )
    if "suno_music_open" in source_names:
        special["models"]["suno_music_open"] = fixed_entry_rule(
            "音乐生成",
            "按功能固定计费；免费功能显式为 0",
            ["feature"],
            [
                {"key": "create", "feature": "发起创作", "credits": 10},
                {"key": "sound_effect", "feature": "生成音效", "credits": 2},
                {"key": "lyrics", "feature": "生成歌词", "credits": 0},
                {"key": "style", "feature": "提升音乐风格", "credits": 0},
                {"key": "upload", "feature": "上传参考音频", "credits": 0},
                {"key": "persona", "feature": "创建歌手风格(Persona)", "credits": 0},
            ],
            credit_unit_price,
            columns=[{"key": "feature", "title": "功能"}, {"key": "price", "title": "价格"}],
            default_value="create",
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
        display["min_price_text"] = f"{price_text(min_price)} 起" if sections_have_price(display["sections"]) else ""
        display["billing_enabled"] = bool(rule.get("billing_enabled"))
        rule["display"] = display
        special["models"][model_name] = rule

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
    source_names = {row.get("model_name") for row in rows}
    missing_from_fzbl_special_models = sorted(CANK_SPECIAL_MODELS - source_names)
    fzbl_cank_special_models = sorted(CANK_SPECIAL_MODELS & source_names)
    fzbl_cank_special_without_billing = sorted(
        name for name in fzbl_cank_special_models if not (special_models.get(name) or {}).get("billing_enabled")
    )
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
        "missing_from_fzbl_special_models": missing_from_fzbl_special_models,
        "fzbl_cank_special_models": fzbl_cank_special_models,
        "fzbl_cank_special_without_billing": fzbl_cank_special_without_billing,
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
