import unittest
from unittest.mock import mock_open, patch

from probe_calibrate import read_proc_stat, compare_report


class CPUCalibrationTest(unittest.TestCase):
    def test_guest_time_is_already_counted(self):
        with patch("builtins.open", mock_open(read_data="cpu 150 150 0 200 0 0 0 0 70 70\n")):
            total, idle = read_proc_stat()
        self.assertEqual((total, idle), (500, 200))
        # Independent reference: user+nice rose by 100; idle rose by 100.
        self.assertEqual((1 - (idle - 100) / (total - 300)) * 100, 50)

    def test_counter_reset_is_unknown(self):
        before = {"cpu_total": 100, "cpu_idle": 80}
        after = {"cpu_total": 110, "cpu_idle": 1, "mem_total": 1, "mem_available": 1,
                 "disk_total": 1, "disk_used": 0, "net_up": 0, "net_down": 0, "uptime": 1}
        before.update({"net_up": 0, "net_down": 0, "uptime": 0})
        cpu = compare_report({"cpu": {"usage": 0}}, before, after, 1)[0]
        self.assertIsNone(cpu["expected"])
        self.assertIn("NOT RUN", cpu["detail"])


if __name__ == "__main__":
    unittest.main()
