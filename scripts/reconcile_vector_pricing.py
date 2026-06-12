#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import os
import tempfile
import urllib.request
from copy import deepcopy
from pathlib import Path
from typing import Any


SCRIPT_DIR = Path(__file__).resolve().parent
DEFAULT_INPUT = SCRIPT_DIR.parent.parent / "向量普通规则.txt"
DEFAULT_OUTPUT = (
    SCRIPT_DIR.parent.parent / "向量普通规则.remote-aligned.txt"
)
DEFAULT_REPORT = SCRIPT_DIR.parent / "tmp" / "price-align" / "reconcile-report.json"
DEFAULT_BASE_URL = "https://q.aibaotui.com"


def read_json_utf8(path: Path) -> dict[str, Any]:
    raw = path.read_bytes()
    try:
        text = raw.decode("utf-8-sig")
    except UnicodeDecodeError as exc:
        raise ValueError(f"文件不是有效 UTF-8: {path}: {exc}") from exc
    try:
        value = json.loads(text)
    except json.JSONDecodeError as exc:
        raise ValueError(f"文件不是有效 JSON: {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"JSON 顶层必须是对象: {path}")
    return value


def fetch_remote_pricing(base_url: str) -> dict[str, Any]:
    url = base_url.rstrip("/") + "/api/pricing"
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/json",
            "User-Agent": "new-api-vector-pricing-reconciler/1.0",
        },
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        if response.status != 200:
            raise ValueError(f"远端价格接口返回 HTTP {response.status}")
        content = response.read(16 * 1024 * 1024 + 1)
    if len(content) > 16 * 1024 * 1024:
        raise ValueError("远端价格响应超过 16 MiB")
    try:
        value = json.loads(content.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"远端价格接口返回无效 JSON: {exc}") from exc
    if not isinstance(value, dict) or value.get("success") is not True:
        raise ValueError("远端价格接口返回失败")
    return value


def reconcile_vector_pricing(
    normal: dict[str, Any],
    remote: dict[str, Any],
) -> tuple[dict[str, Any], dict[str, Any]]:
    rows = normal.get("data")
    if not isinstance(rows, list):
        raise ValueError("普通规则缺少 data 数组")
    remote_data = remote.get("data")
    if remote.get("success") is not True or not isinstance(remote_data, dict):
        raise ValueError("远端价格数据无效")
    group_special = remote_data.get("group_special")
    model_group = remote_data.get("model_group")
    completion_ratios = remote_data.get("model_completion_ratio")
    if not isinstance(group_special, dict) or not isinstance(model_group, dict):
        raise ValueError("远端价格缺少 group_special 或 model_group")
    if not isinstance(completion_ratios, dict):
        completion_ratios = {}

    aligned = deepcopy(normal)
    aligned_rows: list[dict[str, Any]] = []
    removed_models: list[str] = []
    changed_models: list[dict[str, Any]] = []
    group_ratios = aligned.setdefault("group_ratio", {})
    if not isinstance(group_ratios, dict):
        raise ValueError("普通规则 group_ratio 必须是对象")

    for raw_item in rows:
        if not isinstance(raw_item, dict):
            raise ValueError("普通规则 data 包含非对象行")
        item = deepcopy(raw_item)
        model_name = str(item.get("model_name") or "").strip()
        if not model_name:
            raise ValueError("普通规则存在空 model_name")
        remote_groups = group_special.get(model_name)
        if not isinstance(remote_groups, list) or not remote_groups:
            removed_models.append(model_name)
            continue

        normalized_groups = sorted(
            {
                str(group).strip()
                for group in remote_groups
                if str(group).strip()
            }
        )
        if not normalized_groups:
            removed_models.append(model_name)
            continue

        price_types: set[int] = set()
        base_prices: set[float] = set()
        for group_name in normalized_groups:
            group = model_group.get(group_name)
            if not isinstance(group, dict):
                raise ValueError(f"{model_name}: 远端缺少分组 {group_name}")
            group_ratio = _number(group.get("GroupRatio"), f"{group_name}.GroupRatio")
            group_ratios[group_name] = group_ratio
            prices = group.get("ModelPrice")
            price = prices.get(model_name) if isinstance(prices, dict) else None
            if not isinstance(price, dict):
                raise ValueError(f"{model_name}: 远端分组 {group_name} 缺少价格")
            price_types.add(int(_number(price.get("priceType"), "priceType")))
            base_prices.add(_number(price.get("price"), "price"))

        if len(price_types) != 1:
            raise ValueError(f"{model_name}: 远端分组计费类型不一致")
        if len(base_prices) != 1:
            raise ValueError(f"{model_name}: 远端分组基础价格不一致")

        price_type = next(iter(price_types))
        base_price = next(iter(base_prices))
        before = {
            "quota_type": item.get("quota_type", 0),
            "model_ratio": item.get("model_ratio", 0),
            "model_price": item.get("model_price", 0),
            "completion_ratio": item.get("completion_ratio", 0),
            "enable_groups": sorted(item.get("enable_groups") or []),
        }
        item["quota_type"] = price_type
        item["enable_groups"] = normalized_groups
        if price_type == 0:
            if model_name not in completion_ratios:
                raise ValueError(f"{model_name}: 远端缺少 completion_ratio")
            item["model_ratio"] = base_price
            item["model_price"] = 0
            item["completion_ratio"] = _number(
                completion_ratios[model_name],
                f"{model_name}.completion_ratio",
            )
        else:
            item["model_ratio"] = 0
            item["model_price"] = base_price

        after = {
            "quota_type": item.get("quota_type", 0),
            "model_ratio": item.get("model_ratio", 0),
            "model_price": item.get("model_price", 0),
            "completion_ratio": item.get("completion_ratio", 0),
            "enable_groups": sorted(item.get("enable_groups") or []),
        }
        if before != after:
            changed_models.append(
                {
                    "model_name": model_name,
                    "before": before,
                    "after": after,
                }
            )
        aligned_rows.append(item)

    aligned["data"] = aligned_rows
    report = {
        "status": "success",
        "input_models": len(rows),
        "output_models": len(aligned_rows),
        "updated_models": len(changed_models),
        "removed_models": sorted(removed_models),
        "changes": changed_models,
    }
    return aligned, report


def _number(value: Any, field: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{field} 不是有效数字")
    return float(value)


def atomic_write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = json.dumps(value, ensure_ascii=False, indent=2) + "\n"
    fd, temp_name = tempfile.mkstemp(
        prefix=f".{path.name}.",
        suffix=".tmp",
        dir=path.parent,
    )
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


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="按供应商 /api/pricing 修正向量普通规则，不覆盖原始文件。"
    )
    parser.add_argument("--input", type=Path, default=DEFAULT_INPUT)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--report", type=Path, default=DEFAULT_REPORT)
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument(
        "--remote-json",
        type=Path,
        help="使用本地远端价格响应，测试或离线复核时使用。",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    normal = read_json_utf8(args.input)
    remote = (
        read_json_utf8(args.remote_json)
        if args.remote_json
        else fetch_remote_pricing(args.base_url)
    )
    aligned, report = reconcile_vector_pricing(normal, remote)
    report["input"] = str(args.input.resolve())
    report["output"] = str(args.output.resolve())
    report["normal_sha256"] = hashlib.sha256(
        args.input.read_bytes()
    ).hexdigest()
    report["remote_sha256"] = hashlib.sha256(
        json.dumps(remote, ensure_ascii=False, sort_keys=True).encode("utf-8")
    ).hexdigest()
    atomic_write_json(args.output, aligned)
    atomic_write_json(args.report, report)
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
