import json
import tempfile
import unittest
from pathlib import Path

from scripts.clean_special_pricing import (
    clean_special_pricing,
    discover_special_models,
)


class SpecialPricingCleanerTest(unittest.TestCase):
    def test_discovers_comparison_and_ternary_models(self):
        source = """
        else if ("viduq2" === model) { renderPrice(); }
        "sora-2" === model || "sora-2-pro" === model ? rows.push({}) : null;
        if (new Set(["pixverse-video", "pixverse-upload"]).has(item?.model_name)) {
          renderSpecial();
        }
        """

        self.assertEqual(
            discover_special_models(source),
            {
                "pixverse-upload",
                "pixverse-video",
                "sora-2",
                "sora-2-pro",
                "viduq2",
            },
        )

    def test_unknown_model_blocks_formal_output(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            source_path = root / "special.js"
            output_path = root / "cleaned.json"
            report_path = root / "report.json"
            source_path.write_text(
                'else if ("new-unknown-special-model" === model) { rows.push({price: 1}); }',
                encoding="utf-8",
            )

            result = clean_special_pricing(
                input_path=source_path,
                output_path=output_path,
                report_path=report_path,
            )

            self.assertFalse(result.success)
            self.assertFalse(output_path.exists())
            self.assertTrue(report_path.exists())
            report = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertIn("new-unknown-special-model", report["uncovered_models"])

    def test_failure_does_not_replace_existing_output(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            source_path = root / "special.js"
            output_path = root / "cleaned.json"
            report_path = root / "report.json"
            original_output = '{"status":"previous-success"}\n'
            source_path.write_text(
                'else if ("new-unknown-special-model" === model) { rows.push({price: 1}); }',
                encoding="utf-8",
            )
            output_path.write_text(original_output, encoding="utf-8")

            result = clean_special_pricing(
                input_path=source_path,
                output_path=output_path,
                report_path=report_path,
            )

            self.assertFalse(result.success)
            self.assertEqual(original_output, output_path.read_text(encoding="utf-8"))
            report = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertEqual("blocked", report["status"])

    def test_current_vector_fixture_has_complete_coverage(self):
        fixture = Path(__file__).resolve().parents[2] / "向量特殊规则.txt"
        if not fixture.exists():
            self.skipTest(f"fixture not found: {fixture}")

        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            output_path = root / "cleaned.json"
            report_path = root / "report.json"

            result = clean_special_pricing(
                input_path=fixture,
                output_path=output_path,
                report_path=report_path,
            )

            self.assertTrue(result.success, result.errors)
            self.assertTrue(output_path.exists())
            document = json.loads(output_path.read_text(encoding="utf-8"))
            self.assertEqual(result.discovered_models, set(document["models"]))
            self.assertEqual(55, len(document["models"]))
            for model_name, rule in document["models"].items():
                self.assertIsInstance(rule["billing_enabled"], bool, model_name)
                if not rule["billing_enabled"]:
                    self.assertTrue(rule["display_only_reason"], model_name)


if __name__ == "__main__":
    unittest.main()
