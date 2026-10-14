"""Test token validation and private settings updates with synthetic credentials only."""

import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "onemap_setup",
    Path(__file__).resolve().parents[1] / "setup-onemap.py",
)

setup = importlib.util.module_from_spec(spec)

spec.loader.exec_module(setup)


class TokenSetupTests(unittest.TestCase):
    """Use temporary files; these tests never call the real OneMap service."""

    def test_rejects_invalid_or_expired_authentication_responses(self):
        valid = {"access_token": "synthetic.auth.token", "expiry_timestamp": "2000"}

        token, _ = setup.parse_token(valid, now=1000)

        self.assertEqual(token, "synthetic.auth.token")

        for body in [
            None,
            {**valid, "access_token": "token\nFAREWISE_ADDR=bad"},

            {**valid, "access_token": '$(evil).auth.token'},

            {**valid, "expiry_timestamp": 999},
            {**valid, "expiry_timestamp": True},

            {**valid, "expiry_timestamp": "not-a-date"},
        ]:
            with self.assertRaises(ValueError):
                setup.parse_token(body, now=1000)

    def test_saves_only_token_and_preserves_other_configuration(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"

            path.write_text("# My config\nFAREWISE_ADDR=127.0.0.1:8081\nFAREWISE_ROUTING_PROVIDER=approximate\nexport ONEMAP_TOKEN=old\n")

            setup.save_token(path, "synthetic.auth.token")

            text = path.read_text()

            self.assertIn("FAREWISE_ADDR=127.0.0.1:8081", text)

            self.assertIn("FAREWISE_ROUTING_PROVIDER=onemap", text)

            self.assertEqual(text.count("ONEMAP_TOKEN="), 1)

            self.assertIn("ONEMAP_TOKEN=synthetic.auth.token", text)

            self.assertNotIn("password", text)

            self.assertNotIn("email", text)

            self.assertEqual(os.stat(path).st_mode & 0o777, 0o600)

            self.assertEqual(list(Path(directory).iterdir()), [path])

    def test_invalid_token_cannot_change_an_existing_configuration(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"

            path.write_text("FAREWISE_ROUTING_PROVIDER=approximate\n")

            before = path.read_text()

            with self.assertRaises(ValueError):
                setup.save_token(path, "x\nONEMAP_TOKEN=unsafe")

            self.assertEqual(path.read_text(), before)


if __name__ == "__main__":
    unittest.main()
