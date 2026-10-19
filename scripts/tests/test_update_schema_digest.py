import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
UPDATER = ROOT / "scripts" / "update-schema-digest.py"


class UpdateSchemaDigestTest(unittest.TestCase):
    def run_updater(self, contents: str, schema: str = "user-v1"):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / "api-bff.yaml"
            config.write_text(contents)
            result = subprocess.run(
                ["python3", str(UPDATER), str(config), schema, "user-new"],
                capture_output=True,
                text=True,
            )
            return result, config.read_text()

    def test_updates_only_the_named_schema(self):
        result, updated = self.run_updater(
            """GenericRPC:
  Schemas:
    - Name: user-v1
      DescriptorSHA256: user-old
    - Name: account-v1
      DescriptorSHA256: account-old
"""
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("DescriptorSHA256: user-new", updated)
        self.assertIn("DescriptorSHA256: account-old", updated)

    def test_missing_digest_does_not_modify_the_next_schema(self):
        original = """GenericRPC:
  Schemas:
    - Name: user-v1
      Service: user-service
    - Name: account-v1
      DescriptorSHA256: account-old
"""

        result, updated = self.run_updater(original)

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(updated, original)

    def test_duplicate_schema_name_is_rejected(self):
        original = """GenericRPC:
  Schemas:
    - Name: user-v1
      DescriptorSHA256: first
    - Name: user-v1
      DescriptorSHA256: second
"""

        result, updated = self.run_updater(original)

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(updated, original)


if __name__ == "__main__":
    unittest.main()
