#!/usr/bin/env python3
"""Generate a guarded channel-key import from an upstream token Excel file.

Default mode is dry-run only: parse the xlsx, validate rows, and write a masked
report plus SQL. Applying the SQL is intentionally a separate confirmed step.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import time
import zipfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from xml.etree import ElementTree


REQUIRED_HEADERS = ["名称", "状态", "分组", "密钥（sk-前缀）", "可用模型"]
DEFAULT_CONFIRM_TEXT = "UPDATE_CHANNEL_KEYS"


@dataclass(frozen=True)
class TokenRow:
    row_number: int
    name: str
    status: str
    group: str
    key: str
    models: str


def read_zip_text(zf: zipfile.ZipFile, name: str) -> str:
    with zf.open(name) as f:
        return f.read().decode("utf-8")


def ns_tag(name: str) -> str:
    return f"{{http://schemas.openxmlformats.org/spreadsheetml/2006/main}}{name}"


def column_index(cell_ref: str) -> int:
    match = re.match(r"^[A-Z]+", cell_ref or "")
    if not match:
        return 0
    value = 0
    for char in match.group(0):
        value = value * 26 + ord(char) - ord("A") + 1
    return value - 1


def read_shared_strings(zf: zipfile.ZipFile) -> list[str]:
    try:
        root = ElementTree.fromstring(read_zip_text(zf, "xl/sharedStrings.xml"))
    except KeyError:
        return []
    strings: list[str] = []
    for si in root.findall(ns_tag("si")):
        pieces = [node.text or "" for node in si.iter(ns_tag("t"))]
        strings.append("".join(pieces))
    return strings


def cell_value(cell: ElementTree.Element, shared_strings: list[str]) -> str:
    value_node = cell.find(ns_tag("v"))
    raw_value = value_node.text if value_node is not None and value_node.text is not None else ""
    cell_type = cell.attrib.get("t")
    if cell_type == "s" and raw_value != "":
        return shared_strings[int(raw_value)]
    if cell_type == "inlineStr":
        pieces = [node.text or "" for node in cell.iter(ns_tag("t"))]
        return "".join(pieces)
    return raw_value


def read_sheet_rows(path: Path) -> list[list[str]]:
    with zipfile.ZipFile(path) as zf:
        shared_strings = read_shared_strings(zf)
        sheet = ElementTree.fromstring(read_zip_text(zf, "xl/worksheets/sheet1.xml"))
    rows: list[list[str]] = []
    sheet_data = sheet.find(ns_tag("sheetData"))
    if sheet_data is None:
        return rows
    for row in sheet_data.findall(ns_tag("row")):
        values: dict[int, str] = {}
        for cell in row.findall(ns_tag("c")):
            values[column_index(cell.attrib.get("r", ""))] = cell_value(cell, shared_strings).strip()
        max_index = max(values.keys(), default=-1)
        rows.append([values.get(index, "") for index in range(max_index + 1)])
    return rows


def parse_token_rows(path: Path) -> list[TokenRow]:
    rows = read_sheet_rows(path)
    if not rows:
        raise ValueError("Excel 文件没有可读取的数据")
    headers = rows[0]
    missing = [header for header in REQUIRED_HEADERS if header not in headers]
    if missing:
        raise ValueError(f"Excel 缺少必要列: {', '.join(missing)}")
    index = {header: headers.index(header) for header in REQUIRED_HEADERS}
    parsed: list[TokenRow] = []
    for offset, row in enumerate(rows[1:], start=2):
        def get(header: str) -> str:
            idx = index[header]
            return row[idx].strip() if idx < len(row) else ""

        if not any(cell.strip() for cell in row):
            continue
        parsed.append(
            TokenRow(
                row_number=offset,
                name=get("名称"),
                status=get("状态"),
                group=get("分组"),
                key=get("密钥（sk-前缀）"),
                models=get("可用模型"),
            )
        )
    return parsed


def mask_key(key: str) -> str:
    if len(key) <= 14:
        return key[:4] + "***"
    return key[:10] + "***" + key[-6:]


def sql_str(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def validate_rows(rows: list[TokenRow]) -> tuple[list[TokenRow], list[dict[str, Any]]]:
    usable: list[TokenRow] = []
    errors: list[dict[str, Any]] = []
    keys_by_group: dict[str, str] = {}
    for row in rows:
        row_errors: list[str] = []
        if row.status != "已启用":
            row_errors.append("状态不是已启用")
        if not row.group:
            row_errors.append("分组为空")
        if not row.key.startswith("sk-"):
            row_errors.append("密钥不是 sk- 前缀")
        if row.group and row.group in keys_by_group and keys_by_group[row.group] != row.key:
            row_errors.append("同一分组出现多个不同密钥")
        if row_errors:
            errors.append(
                {
                    "row": row.row_number,
                    "name": row.name,
                    "group": row.group,
                    "key_mask": mask_key(row.key),
                    "errors": row_errors,
                }
            )
            continue
        keys_by_group[row.group] = row.key
        usable.append(row)
    return usable, errors


def build_sql(rows: list[TokenRow], base_url: str, only_fzbl: bool) -> str:
    now = int(time.time())
    lines = [
        "-- Generated by scripts/import_upstream_group_keys.py",
        "-- Review this file before applying. It contains full upstream keys.",
        "BEGIN;",
        "CREATE TEMP TABLE _upstream_group_keys(group_name text PRIMARY KEY, api_key text) ON COMMIT DROP;",
    ]
    for row in rows:
        lines.append(
            "INSERT INTO _upstream_group_keys(group_name, api_key) VALUES "
            f"({sql_str(row.group)}, {sql_str(row.key)}) "
            "ON CONFLICT (group_name) DO UPDATE SET api_key = EXCLUDED.api_key;"
        )
    tag_filter = " AND (channels.tag LIKE 'fzbl-sync%' OR channels.tag LIKE 'fzbl-special%')" if only_fzbl else ""
    lines.extend(
        [
            "UPDATE channels",
            "SET key = _upstream_group_keys.api_key",
            "FROM _upstream_group_keys",
            f"WHERE COALESCE(channels.base_url, '') = {sql_str(base_url)}",
            '  AND channels."group" = _upstream_group_keys.group_name'
            + tag_filter
            + ";",
            "COMMIT;",
            "",
        ]
    )
    lines.insert(2, f"-- Created at unix time {now}")
    return "\n".join(lines)


def write_outputs(out_dir: Path, report: dict[str, Any], sql: str) -> tuple[Path, Path]:
    out_dir.mkdir(parents=True, exist_ok=True)
    report_path = out_dir / "upstream-group-keys-report.json"
    sql_path = out_dir / "upstream-group-keys.sql"
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    sql_path.write_text(sql, encoding="utf-8")
    return report_path, sql_path


def build_report(rows: list[TokenRow], errors: list[dict[str, Any]], base_url: str, only_fzbl: bool) -> dict[str, Any]:
    return {
        "mode": "dry-run",
        "base_url": base_url,
        "channel_scope": "fzbl tags only" if only_fzbl else "all matching base_url and group channels",
        "valid_rows": len(rows),
        "invalid_rows": errors,
        "groups": [
            {
                "row": row.row_number,
                "group": row.group,
                "name": row.name,
                "status": row.status,
                "models": row.models,
                "key_mask": mask_key(row.key),
            }
            for row in rows
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Dry-run import upstream fixed group keys from xlsx.")
    parser.add_argument("--xlsx", required=True, help="Path to 令牌列表_*.xlsx")
    parser.add_argument("--base-url", default="https://q.aibaotui.com/")
    parser.add_argument("--out-dir", default="tmp/upstream-group-keys")
    parser.add_argument("--include-non-fzbl", action="store_true", help="Update all matching channels, not only fzbl tagged channels")
    parser.add_argument("--confirm", default="", help=f"Required text for future apply workflows: {DEFAULT_CONFIRM_TEXT}")
    args = parser.parse_args()

    xlsx_path = Path(args.xlsx)
    if not xlsx_path.exists():
        raise FileNotFoundError(f"找不到 Excel 文件: {xlsx_path}")

    rows, errors = validate_rows(parse_token_rows(xlsx_path))
    if errors:
        report = build_report(rows, errors, args.base_url, not args.include_non_fzbl)
        report_path, _ = write_outputs(Path(args.out_dir), report, "")
        print(f"发现校验错误，已写入报告: {report_path}")
        return 1

    sql = build_sql(rows, args.base_url.rstrip("/"), not args.include_non_fzbl)
    report = build_report(rows, errors, args.base_url.rstrip("/"), not args.include_non_fzbl)
    report_path, sql_path = write_outputs(Path(args.out_dir), report, sql)

    print(f"Excel 分组密钥 dry-run 完成: {len(rows)} 个分组")
    print(f"报告: {report_path}")
    print(f"SQL: {sql_path}")
    print("预览:")
    for row in rows:
        print(f"- {row.group}: {mask_key(row.key)} ({row.name})")
    if args.confirm != DEFAULT_CONFIRM_TEXT:
        print(f"未执行更新。后续真正更新前需要明确确认: --confirm {DEFAULT_CONFIRM_TEXT}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"错误: {exc}", file=sys.stderr)
        raise SystemExit(1)
