"""Offline tests for the pure math of capacity50.py — no server is started."""

import math
import unittest

import capacity50


class PercentileTest(unittest.TestCase):
    def test_known_percentiles(self):
        self.assertEqual(capacity50.percentile([1.0, 2.0, 3.0, 4.0], 50), 2.5)
        self.assertEqual(capacity50.percentile([10.0], 95), 10.0)

    def test_empty_is_nan(self):
        self.assertTrue(math.isnan(capacity50.percentile([], 95)))


class RssStatsTest(unittest.TestCase):
    def flat_series(self, level_kib=70000):
        # 5-second cadence from t=0; warmup at 60 s leaves 12 post-warmup samples.
        return [(float(sec), level_kib) for sec in range(0, 130, 5)]

    def test_flat_series_reports_zero_growth(self):
        stats = capacity50.rss_stats(self.flat_series(), run_start=0.0, warmup=60.0)
        self.assertEqual(stats["growth_mib"], 0.0)
        self.assertEqual(stats["drift_mib_per_day"], 0.0)
        self.assertEqual(stats["peak_mib"], 70000 / 1024)

    def test_rising_series_measures_growth(self):
        samples = self.flat_series()
        for idx, (ts, kb) in enumerate(samples):
            samples[idx] = (ts, kb + idx * 1024)  # +1 MiB per sample
        stats = capacity50.rss_stats(samples, run_start=0.0, warmup=60.0)
        # Post-warmup samples are idx 12..25; first-five mean 14, last-five mean 23.
        self.assertAlmostEqual(stats["growth_mib"], 9.0, places=6)
        self.assertGreater(stats["plateau_growth_mib"], 0)
        self.assertGreater(stats["drift_mib_per_day"], 0)

    def test_plateaued_series_has_zero_plateau_growth(self):
        # The early post-warmup samples settle before the plateau region.
        samples = [(float(sec), 70000 + (400 if 60 <= sec < 80 else 0)) for sec in range(0, 130, 5)]
        stats = capacity50.rss_stats(samples, run_start=0.0, warmup=60.0)
        self.assertEqual(stats["plateau_growth_mib"], 0.0)

    def test_too_few_post_warmup_samples_is_nan(self):
        stats = capacity50.rss_stats([(0.0, 60000), (5.0, 61000)], run_start=0.0, warmup=60.0)
        self.assertTrue(math.isnan(stats["growth_mib"]))
        self.assertEqual(stats["peak_mib"], 61000 / 1024)


if __name__ == "__main__":
    unittest.main()
