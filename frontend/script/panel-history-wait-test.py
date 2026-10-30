"""Regression checks for waiting on the asynchronous startup sample."""

import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("panel_history_spec", Path(__file__).with_name("panel-history.spec.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class StartupSampleTest(unittest.TestCase):
    def test_waits_for_real_pair_after_an_empty_response(self):
        empty = (200, {"Cache-Control": "no-store"}, {"data": {"points": []}})
        present = (200, {"Cache-Control": "no-store"}, {"data": {"points": [
            {"theme_cache_disk": "ready", "theme_cache_memory_loaded": False},
        ]}})
        with patch.object(module.time, "sleep"):
            values = iter([empty, present])
            self.assertEqual(module.wait_for_startup_sample(lambda: next(values)), present[2])

    def test_missing_sample_fails_instead_of_becoming_a_pass(self):
        empty = (200, {"Cache-Control": "no-store"}, {"data": {"points": []}})
        with self.assertRaisesRegex(AssertionError, "sample not visible"):
            module.wait_for_startup_sample(lambda: empty, timeout=0)

    def test_fails_on_invalid_response(self):
        with self.assertRaises(AssertionError):
            module.wait_for_startup_sample(lambda: (200, {}, {"data": {"points": []}}), timeout=0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
