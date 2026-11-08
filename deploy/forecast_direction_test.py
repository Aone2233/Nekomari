#!/usr/bin/env python3
"""Calendar edge cases for the disposable forecast smoke fixture."""

import datetime as dt
import importlib.util
from pathlib import Path
import unittest


spec = importlib.util.spec_from_file_location("forecast_direction_smoke", Path(__file__).with_name("forecast-direction-smoke.py"))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


class ForecastFixtureCalendarTests(unittest.TestCase):
    def test_reset_day_produces_a_usable_cycle_across_calendar(self):
        for year in (2024, 2026, 2100):
            for month in range(1, 13):
                day = dt.date(year, month, 1)
                while day.month == month:
                    now = dt.datetime.combine(day, dt.time(12), tzinfo=dt.timezone.utc)
                    reset = fixture.fixture_reset_day(now)
                    start = fixture.fixture_cycle_start(now, reset)
                    self.assertLessEqual(start, now - dt.timedelta(days=6))
                    self.assertGreater(now - start, dt.timedelta(days=7))
                    self.assertLess(now - start, dt.timedelta(days=15))
                    day += dt.timedelta(days=1)

    def test_short_month_rolls_forward(self):
        now = dt.datetime(2026, 3, 5, tzinfo=dt.timezone.utc)
        self.assertEqual(fixture.fixture_cycle_start(now, 31), dt.datetime(2026, 3, 1, tzinfo=dt.timezone.utc))


if __name__ == "__main__":
    unittest.main()
