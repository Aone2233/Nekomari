"""Offline contract: operator scripts must receive API keys explicitly."""

from contextlib import closing
import json
import os
import sqlite3
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from nekomari_auth import api_key_header


class APIKeyProvisioningTest(unittest.TestCase):
    def test_explicit_and_environment_keys(self):
        with patch.dict(os.environ, {"NEKOMARI_API_KEY": "env-test-key"}):
            self.assertEqual(api_key_header(), {"Authorization": "Bearer env-test-key"})
            self.assertEqual(api_key_header("explicit-test-key"), {"Authorization": "Bearer explicit-test-key"})

    def test_missing_environment_never_recovers_legacy_global_key(self):
        with tempfile.TemporaryDirectory() as temp:
            with closing(sqlite3.connect(Path(temp) / "komari.db")) as connection:
                with connection:
                    connection.execute("CREATE TABLE configs (key TEXT PRIMARY KEY, value TEXT)")
                    connection.execute("INSERT INTO configs VALUES (?, ?)", ("api_key", json.dumps("test-only-old-global-key")))
            with patch.dict(os.environ, {"NEKOMARI_API_KEY": ""}):
                self.assertEqual(api_key_header(data_dir=temp), {})


if __name__ == "__main__":
    unittest.main(verbosity=2)
