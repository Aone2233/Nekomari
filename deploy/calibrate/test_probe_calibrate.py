import unittest
from unittest.mock import mock_open, patch

from probe_calibrate import read_proc_stat, compare_report, expected_ram_used


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

    def test_unknown_quality_and_lifetime_network_counters_never_pass(self):
        before = {"cpu_total": 100, "cpu_idle": 80, "net_up": 0, "net_down": 0}
        after = {"cpu_total": 200, "cpu_idle": 180, "mem_total": 100,
                 "mem_available": 100, "disk_used": 0, "net_up": 100, "net_down": 200}
        report = {"quality": {"cpu": "unknown", "ram": "unknown", "disk": "unknown"},
                  "cpu": {"usage": 0}, "ram": {"used": 0}, "disk": {"used": 0},
                  "network": {"totalUp": 100, "totalDown": 200}}
        results = {item["item"]: item for item in compare_report(report, before, after, 1)}
        for name in ("cpu", "ram_used", "disk_used", "network_delta"):
            self.assertFalse(results[name]["pass"], name)
            self.assertIn("NOT RUN", results[name]["detail"])

    def test_missing_memory_reference_and_boolean_values_never_pass(self):
        before = {"cpu_total": 100, "cpu_idle": 80}
        after = {"cpu_total": 200, "cpu_idle": 180, "mem_total": 100,
                 "mem_available": None, "disk_used": 0}
        report = {"quality": {"cpu": "ok", "ram": "ok", "disk": "ok"},
                  "cpu": {"usage": False}, "ram": {"used": 100}, "disk": {"used": False}}
        results = {item["item"]: item for item in compare_report(report, before, after, 1)}
        for name in ("cpu", "ram_used", "disk_used"):
            self.assertFalse(results[name]["pass"], name)
            self.assertIn("NOT RUN", results[name]["detail"])

    def test_ram_reference_matches_reported_mode(self):
        sample = {"mem_total": 1000, "mem_free": 100, "mem_available": 400,
                  "mem_cached": 200, "mem_sreclaimable": 50,
                  "mem_buffers": 50, "mem_shmem": 20}
        self.assertEqual(expected_ram_used(sample, "gopsutil"), 600)
        self.assertEqual(expected_ram_used(sample, "includeCache"), 900)
        self.assertEqual(expected_ram_used(sample, "htoplike"), 620)
        self.assertIsNone(expected_ram_used(sample, "unknown"))
        del sample["mem_sreclaimable"]
        self.assertIsNone(expected_ram_used(sample, "htoplike"))

    def test_missing_ram_mode_cannot_pass_by_coincidence(self):
        before = {"cpu_total": 100, "cpu_idle": 80}
        after = {"cpu_total": 200, "cpu_idle": 180, "mem_total": 100,
                 "mem_available": 100, "disk_used": 0}
        report = {"quality": {"ram": "ok"}, "ram": {"used": 0}}
        ram = next(item for item in compare_report(report, before, after, 1) if item["item"] == "ram_used")
        self.assertFalse(ram["pass"])
        self.assertIn("NOT RUN", ram["detail"])

    def test_disk_requires_root_only_scope(self):
        before = {"cpu_total": 100, "cpu_idle": 80}
        after = {"cpu_total": 200, "cpu_idle": 180, "mem_total": 100,
                 "mem_available": 100, "disk_used": 50}
        report = {"quality": {"disk": "ok"}, "disk": {"total": 100, "used": 50}}
        def disk():
            return next(item for item in compare_report(report, before, after, 1) if item["item"] == "disk_used")
        self.assertFalse(disk()["pass"])
        report["disk"]["mounts"] = [{"mountpoint": "/data", "total": 100, "used": 50}]
        self.assertFalse(disk()["pass"])
        report["disk"]["mounts"][0]["mountpoint"] = "/"
        self.assertTrue(disk()["pass"])
        report["disk"]["mounts"].append({"mountpoint": "/data", "total": 1, "used": 0})
        self.assertFalse(disk()["pass"])


if __name__ == "__main__":
    unittest.main()
