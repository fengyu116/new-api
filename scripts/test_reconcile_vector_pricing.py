import json
import tempfile
import unittest
from pathlib import Path

from scripts.reconcile_vector_pricing import reconcile_vector_pricing


class ReconcileVectorPricingTest(unittest.TestCase):
    def test_reconciles_groups_prices_and_removes_missing_models(self):
        normal = {
            "group_ratio": {"default": 1, "vip": 2},
            "usable_group": {"default": "默认", "vip": "VIP"},
            "data": [
                {
                    "model_name": "gpt-test",
                    "quota_type": 0,
                    "model_ratio": 1,
                    "model_price": 0,
                    "completion_ratio": 2,
                    "enable_groups": ["default", "vip"],
                },
                {
                    "model_name": "removed-model",
                    "quota_type": 1,
                    "model_price": 3,
                    "enable_groups": ["default"],
                },
            ],
        }
        remote = {
            "success": True,
            "data": {
                "model_completion_ratio": {"gpt-test": 3},
                "group_special": {"gpt-test": ["vip"]},
                "model_group": {
                    "vip": {
                        "GroupRatio": 1.5,
                        "ModelPrice": {
                            "gpt-test": {"priceType": 0, "price": 2.5}
                        },
                    }
                },
            },
        }

        aligned, report = reconcile_vector_pricing(normal, remote)

        self.assertEqual(1, len(aligned["data"]))
        model = aligned["data"][0]
        self.assertEqual("gpt-test", model["model_name"])
        self.assertEqual(["vip"], model["enable_groups"])
        self.assertEqual(2.5, model["model_ratio"])
        self.assertEqual(0, model["model_price"])
        self.assertEqual(3, model["completion_ratio"])
        self.assertEqual(1.5, aligned["group_ratio"]["vip"])
        self.assertEqual(["removed-model"], report["removed_models"])
        self.assertEqual(1, report["updated_models"])

    def test_blocks_when_remote_groups_disagree_on_base_price(self):
        normal = {
            "data": [
                {
                    "model_name": "gpt-test",
                    "quota_type": 0,
                    "model_ratio": 1,
                    "completion_ratio": 2,
                    "enable_groups": ["default", "vip"],
                }
            ]
        }
        remote = {
            "success": True,
            "data": {
                "model_completion_ratio": {"gpt-test": 2},
                "group_special": {"gpt-test": ["default", "vip"]},
                "model_group": {
                    "default": {
                        "GroupRatio": 1,
                        "ModelPrice": {
                            "gpt-test": {"priceType": 0, "price": 1}
                        },
                    },
                    "vip": {
                        "GroupRatio": 0.8,
                        "ModelPrice": {
                            "gpt-test": {"priceType": 0, "price": 2}
                        },
                    },
                },
            },
        }

        with self.assertRaisesRegex(ValueError, "分组基础价格不一致"):
            reconcile_vector_pricing(normal, remote)

    def test_writes_utf8_output_without_overwriting_input(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            input_path = root / "向量普通规则.txt"
            output_path = root / "向量普通规则.remote-aligned.txt"
            report_path = root / "report.json"
            original = {
                "group_ratio": {"default": 1},
                "usable_group": {"default": "默认"},
                "data": [
                    {
                        "model_name": "gpt-test",
                        "quota_type": 0,
                        "model_ratio": 1,
                        "completion_ratio": 2,
                        "enable_groups": ["default"],
                    }
                ],
            }
            remote = {
                "success": True,
                "data": {
                    "model_completion_ratio": {"gpt-test": 2},
                    "group_special": {"gpt-test": ["default"]},
                    "model_group": {
                        "default": {
                            "GroupRatio": 1,
                            "ModelPrice": {
                                "gpt-test": {"priceType": 0, "price": 1}
                            },
                        }
                    },
                },
            }
            input_path.write_text(
                json.dumps(original, ensure_ascii=False), encoding="utf-8"
            )

            aligned, report = reconcile_vector_pricing(original, remote)
            output_path.write_text(
                json.dumps(aligned, ensure_ascii=False, indent=2) + "\n",
                encoding="utf-8",
            )
            report_path.write_text(
                json.dumps(report, ensure_ascii=False, indent=2) + "\n",
                encoding="utf-8",
            )

            self.assertEqual(
                original,
                json.loads(input_path.read_text(encoding="utf-8")),
            )
            self.assertIn(
                "默认",
                output_path.read_text(encoding="utf-8"),
            )


if __name__ == "__main__":
    unittest.main()
