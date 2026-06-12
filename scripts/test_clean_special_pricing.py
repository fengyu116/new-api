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
                normal_path=None,
                output_path=output_path,
                report_path=report_path,
            )

            self.assertFalse(result.success)
            self.assertFalse(output_path.exists())
            self.assertTrue(report_path.exists())
            report = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertIn("new-unknown-special-model", report["uncovered_models"])

    def test_normal_catalog_filters_special_models_not_in_normal_rules(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            source_path = root / "special.js"
            normal_path = root / "normal.json"
            output_path = root / "cleaned.json"
            report_path = root / "report.json"
            source_path.write_text(
                """
                else if ("gemini-3-pro-image" === model || "gemini-3-pro-image-preview" === model) { rows.push({price: 1}); }
                else if ("sora-2-pro" === model) { rows.push({price: 1}); }
                """,
                encoding="utf-8",
            )
            normal_path.write_text(
                json.dumps(
                    {
                        "data": [
                            {"model_name": "gemini-3-pro-image"},
                            {"model_name": "gemini-3-pro-image-preview"},
                        ]
                    },
                    ensure_ascii=False,
                ),
                encoding="utf-8",
            )

            result = clean_special_pricing(
                input_path=source_path,
                normal_path=normal_path,
                output_path=output_path,
                report_path=report_path,
            )

            self.assertTrue(result.success, result.errors)
            document = json.loads(output_path.read_text(encoding="utf-8"))
            self.assertIn("gemini-3-pro-image", document["models"])
            self.assertIn("gemini-3-pro-image-preview", document["models"])
            self.assertNotIn("sora-2-pro", document["models"])
            report = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertIn("sora-2-pro", report["filtered_out_models"])

    def test_gemini_image_short_names_reuse_preview_rules(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            source_path = root / "special.js"
            output_path = root / "cleaned.json"
            report_path = root / "report.json"
            source_path.write_text(
                """
                else if ("gemini-3-pro-image" === model || "gemini-3-pro-image-preview" === model) { rows.push({price: 1}); }
                else if ("gemini-3.1-flash-image" === model || "gemini-3.1-flash-image-preview" === model) { rows.push({price: 1}); }
                """,
                encoding="utf-8",
            )

            result = clean_special_pricing(
                input_path=source_path,
                normal_path=None,
                output_path=output_path,
                report_path=report_path,
            )

            self.assertTrue(result.success, result.errors)
            document = json.loads(output_path.read_text(encoding="utf-8"))
            self.assertIn("gemini-3-pro-image", document["models"])
            self.assertIn("gemini-3-pro-image-preview", document["models"])
            self.assertIn("gemini-3.1-flash-image", document["models"])
            self.assertIn("gemini-3.1-flash-image-preview", document["models"])

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
                normal_path=None,
                output_path=output_path,
                report_path=report_path,
            )

            self.assertFalse(result.success)
            self.assertEqual(original_output, output_path.read_text(encoding="utf-8"))
            report = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertEqual("blocked", report["status"])

    def test_quota_type_four_rules_use_normal_catalog_base_price(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            source_path = root / "special.js"
            normal_path = root / "normal.json"
            output_path = root / "cleaned.json"
            report_path = root / "report.json"
            source_path.write_text(
                'if (4 === model.quota_type) { renderDurationPrice(); }',
                encoding="utf-8",
            )
            normal_path.write_text(
                json.dumps(
                    {
                        "data": [
                            {
                                "model_name": "happyhorse-1.0-t2v",
                                "quota_type": 4,
                                "model_price": 0.014,
                            },
                            {
                                "model_name": "wan2.6-i2v",
                                "quota_type": 4,
                                "model_price": 1,
                            },
                            {
                                "model_name": "kling-motion-control",
                                "quota_type": 4,
                                "model_price": 0.017,
                            },
                            {
                                "model_name": "future-duration-model",
                                "quota_type": 4,
                                "model_price": 0.1,
                            },
                        ]
                    },
                    ensure_ascii=False,
                ),
                encoding="utf-8",
            )

            result = clean_special_pricing(
                input_path=source_path,
                normal_path=normal_path,
                output_path=output_path,
                report_path=report_path,
            )

            self.assertTrue(result.success, result.errors)
            models = json.loads(output_path.read_text(encoding="utf-8"))["models"]
            happyhorse = {entry["key"]: entry for entry in models["happyhorse-1.0-t2v"]["entries"]}
            self.assertAlmostEqual(1.26, happyhorse["720p"]["first_second_price"])
            self.assertAlmostEqual(2.24, happyhorse["1080p"]["first_second_price"])
            wan = {entry["key"]: entry for entry in models["wan2.6-i2v"]["entries"]}
            self.assertAlmostEqual(3, wan["720p|5"]["price"])
            self.assertAlmostEqual(15, wan["1080p|15"]["price"])
            kling = {entry["key"]: entry for entry in models["kling-motion-control"]["entries"]}
            self.assertAlmostEqual(0.017, kling["v2.6|std"]["first_second_price"])
            self.assertAlmostEqual(0.0408, kling["v3.0|pro"]["first_second_price"])
            generic = models["future-duration-model"]
            self.assertTrue(generic["billing_enabled"])
            self.assertAlmostEqual(0.1, generic["entries"][0]["first_second_price"])

    def test_quota_type_four_without_source_structure_blocks_output(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            source_path = root / "special.js"
            normal_path = root / "normal.json"
            output_path = root / "cleaned.json"
            report_path = root / "report.json"
            source_path.write_text(
                'else if ("viduq2" === model) { renderPrice(); }',
                encoding="utf-8",
            )
            normal_path.write_text(
                json.dumps(
                    {
                        "data": [
                            {
                                "model_name": "future-duration-model",
                                "quota_type": 4,
                                "model_price": 0.1,
                            }
                        ]
                    }
                ),
                encoding="utf-8",
            )

            result = clean_special_pricing(
                input_path=source_path,
                normal_path=normal_path,
                output_path=output_path,
                report_path=report_path,
            )

            self.assertFalse(result.success)
            self.assertFalse(output_path.exists())
            self.assertTrue(
                any("quota_type=4" in error for error in result.errors),
                result.errors,
            )

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
            self.assertNotIn("sora-2", document["models"])
            self.assertNotIn("sora-2-pro", document["models"])
            self.assertIn("gemini-3-pro-image", document["models"])
            self.assertIn("gemini-3.1-flash-image", document["models"])
            for model_name, rule in document["models"].items():
                self.assertIsInstance(rule["billing_enabled"], bool, model_name)
                if not rule["billing_enabled"]:
                    self.assertTrue(rule["display_only_reason"], model_name)


if __name__ == "__main__":
    unittest.main()
