package notifier

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	cache "github.com/patrickmn/go-cache"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/models"
	messageevent "github.com/Aone2233/nekomari/database/models/messageEvent"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/cycle"
	"github.com/Aone2233/nekomari/internal/forecast"
	metricstore "github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/Aone2233/nekomari/utils/messageSender"
)

// Traffic forecasting, wired to the existing traffic notification.
//
// CheckTraffic already warns when a node has *used* a percentage of its allowance. This warns when it
// is *projected to* reach it, which is a different and more useful question: a node at 60% on day five
// of a cycle is a problem, and a node at 60% on day twenty-nine is not, and the percentage alone cannot
// tell them apart.
//
// The two coexist. The used-percentage path keeps its five-percent stepping and its own cache, and this
// one fires once per cycle per node, so a node that is projected to overshoot produces one forecast
// warning and then the usual escalating used-percentage warnings as it climbs — which is the right
// order of information: "you will run out" first, then "you are running out".

// forecastCache records the cycle a node's projection warning was sent for.
//
// Keyed by client, valued by the cycle's start instant, so it is self-invalidating: when the node's
// cycle rotates, the stored start no longer matches and the next projection is allowed to warn. That is
// the "once per cycle, not once per scan" requirement expressed as a value rather than as a timer, and
// it means a restart cannot produce a duplicate inside the same cycle only if the cache survives — which
// it does not, deliberately: an in-memory cache that forgets at worst sends one extra warning, while a
// persisted one would need a migration and a table for a single timestamp per node.
var forecastCache = cache.New(40*24*time.Hour, time.Hour)

// resetDay reads the configured billing-cycle day.
//
// A panel-level setting rather than a per-node one, matching how the agent is deployed: `--month-rotate`
// is a flag, so a fleet set up together shares it. The limitation is real and is stated here rather than
// hidden: the panel computes the cycle in the *server's* timezone, because the agent does not report its
// own. For a fleet whose nodes run UTC — which is the common deployment, and what this one does — the two
// agree exactly. A node in another timezone will have its cycle boundary off by the offset, which shifts
// the elapsed fraction by that much and no more.
func resetDay() int {
	day, err := config.GetAs[int](config.TrafficMonthRotateKey, 1)
	if err != nil {
		logger.ErrorArgs("notifier", "failed to read the traffic cycle day, assuming the 1st:", err)
		return 1
	}
	if day < 1 || day > 31 {
		// The agent ignores an out-of-range day too, so refusing here keeps the two consistent instead of
		// reporting a projection against a window nobody uses.
		return 1
	}
	return day
}

// CheckTrafficForecast projects each node's cycle traffic and warns once per cycle when the projection
// reaches its limit. Called on the same one-minute schedule as CheckTraffic.
func CheckTrafficForecast() {
	now := time.Now()

	allClients, err := clients.GetAllClientBasicInfo()
	if err != nil {
		logger.ErrorArgs("notifier", "failed to list clients for the traffic forecast:", err)
		return
	}

	day := resetDay()

	for _, client := range allClients {
		// A node with no allowance is not forecast at all, rather than forecast against infinity.
		if client.TrafficLimit <= 0 {
			continue
		}
		if !forecastEnabledFor(client.UUID) {
			continue
		}
		projection, cycleStart, err := projectClient(now, client, day)
		if err != nil {
			logger.ErrorArgs("notifier", "traffic forecast failed for "+client.UUID+":", err)
			continue
		}
		if projection.Method == forecast.MethodInsufficient || projection.Warning == nil {
			continue
		}

		// Once per cycle: the stored value is the cycle this warning was sent for.
		key := "trafficforecast:" + client.UUID
		if sent, found := forecastCache.Get(key); found {
			if sentCycle, ok := sent.(time.Time); ok && sentCycle.Equal(cycleStart) {
				continue
			}
		}
		forecastCache.SetDefault(key, cycleStart)

		message := projection.Warning.Message
		if prediction := describeProjection(projection); prediction != "" {
			message = prediction
		}
		if err := messageSender.SendNotification(models.EventMessage{
			Event:   messageevent.Traffic,
			Clients: []models.Client{client},
			Time:    now.UTC(),
			Emoji:   "📈",
			Message: message,
		}); err != nil {
			logger.ErrorArgs("notifier", "failed to send the traffic forecast for "+client.UUID+":", err)
			// The cache entry is removed so the next scan retries rather than waiting a whole cycle: a
			// transport failure is not a reason to stay silent for a month.
			forecastCache.Delete(key)
		}
	}
}

// describeProjection renders the projection with its basis, which is the part that makes it actable.
func describeProjection(projection forecast.Forecast) string {
	parts := []string{fmt.Sprintf("projected %s of %s by the end of the cycle (%.0f%%)",
		humanBytes(projection.ProjectedBytes), humanBytes(limitOf(projection)), projection.ProjectedFraction*100)}
	if projection.CrossesInCycle && !projection.CrossesAt.IsZero() {
		parts = append(parts, fmt.Sprintf("reaching the limit in about %.1f days", projection.CrossesInDays))
	}
	parts = append(parts, fmt.Sprintf("basis: %.2f GiB/day from %d samples over %.0f%% of the cycle, ±%.0f%%",
		projection.Basis.BytesPerDay/(1024*1024*1024), projection.Basis.Samples,
		projection.Basis.Coverage*100, projection.Basis.Uncertainty*100))
	return strings.Join(parts, "; ")
}

// limitOf recovers the limit the projection was measured against, from the fraction it reported.
//
// The forecast does not carry the limit itself — it carries the fraction, which is the thing callers
// reason about — so this division recovers it for the message. A zero fraction means no limit was set
// and no warning would have been produced.
func limitOf(projection forecast.Forecast) int64 {
	if projection.ProjectedFraction <= 0 {
		return 0
	}
	return int64(float64(projection.ProjectedBytes) / projection.ProjectedFraction)
}

// forecastEnabledFor reports whether the node has traffic notification switched on.
//
// The forecast rides the same switch as the used-percentage warning rather than adding a second one: an
// operator who muted traffic notifications for a node does not want a different kind of traffic
// notification from the same panel.
func forecastEnabledFor(uuid string) bool {
	_, enabled := getNotificationConfig(uuid)
	return enabled
}

// configuredForecastThreshold reads the warning fraction, defaulting to the package's own.
//
// The same setting as the used-percentage warning, so the panel has one answer to "when should I hear
// about traffic". A value outside (0, 1] is ignored rather than clamped, because a threshold of 0 would
// warn about every node every cycle and a threshold above 1 would warn about none.
func ForecastThreshold() float64 { return configuredForecastThreshold() }

// configuredForecastThreshold reads the warning fraction from the shared traffic setting.
func configuredForecastThreshold() float64 {
	percentage, err := config.GetAs[float64](config.TrafficLimitPercentageKey, 80.0)
	if err != nil || percentage <= 0 || percentage > 100 {
		return forecast.DefaultThreshold
	}
	return percentage / 100
}

// projectClient reads a node's cycle samples and forecasts them.
//
// Returns the cycle start alongside the projection so the caller can use it as the once-per-cycle key
// without recomputing it, which would be a second place to get the boundary wrong.
func ProjectClient(now time.Time, client models.Client, day int) (forecast.Forecast, error) {
	projection, _, err := projectClient(now, client, day)
	return projection, err
}

// projectClient is the internal form: it also returns the cycle start the caller needs as its
// once-per-cycle key.
func projectClient(now time.Time, client models.Client, day int) (forecast.Forecast, time.Time, error) {
	window, err := cycle.Bounds(day, time.Local, now)
	if err != nil {
		return forecast.Forecast{}, time.Time{}, err
	}

	samples, err := cycleTrafficSamples(now, client, window)
	if err != nil {
		return forecast.Forecast{}, window.Start, err
	}

	projection := forecast.Compute(now, forecast.Request{
		CycleStart: window.Start,
		CycleEnd:   window.End,
		Samples:    samples,
		LimitBytes: client.TrafficLimit,
		Threshold:  configuredForecastThreshold(),
	})
	return projection, window.Start, nil
}

// cycleTrafficSamples reads the node's cycle-to-date traffic over its cycle as a time series.
//
// From the metric store rather than from the latest report, because a projection needs a series: the
// report carries one cumulative total, and one number cannot produce a rate.
//
// The series is `traffic.up` / `traffic.down`, which is the agent's CycleUp/CycleDown recorded as a
// gauge (internal/metricstore/report_mapping.go) — the cycle-to-date total the plan limit is written
// against. It used to read `net.total.up` / `net.total.down`, which is the kernel's counter **since
// boot**: the two answer different questions, and the old comment here asserted they were the same
// quantity, which was simply wrong. A node that rebooted mid-cycle restarted its projection from a
// fraction of its real usage, and a node up for longer than one cycle carried pre-cycle traffic into
// the first bucket of the window.
//
// The two are combined per bucket according to the node's limit type, so the series the projection sees
// is the same quantity the limit is checked against — summing them for a `sum` node, taking up for an
// `up` node, and so on. Getting that wrong would project a number the limit does not apply to.
func cycleTrafficSamples(now time.Time, client models.Client, window cycle.Cycle) ([]forecast.Sample, error) {
	return cycleTrafficSamplesWithStore(now, client, window, metricstore.GetStore())
}

func cycleTrafficSamplesWithStore(now time.Time, client models.Client, window cycle.Cycle, store *metric.Store) ([]forecast.Sample, error) {
	if store == nil {
		return nil, fmt.Errorf("metric store is not available")
	}

	interval := sampleInterval(window.Duration())
	specs := make([]metric.BatchSeriesSpec, 0, len(cycleTrafficMetricNames))
	for _, name := range cycleTrafficMetricNames {
		specs = append(specs, metric.BatchSeriesSpec{
			MetricName:   name,
			Aggregations: []metric.Aggregation{metric.AggMax},
			Interval:     interval,
			AlignedStart: true,
		})
	}
	result, err := store.SeriesBatch(context.Background(), metric.BatchSeriesQuery{
		Specs:     specs,
		EntityIDs: []string{client.UUID},
		Start:     window.Start,
		End:       now.UTC(),
	}, now.UTC())
	if err != nil {
		return nil, err
	}

	return cycleSamplesFromSeries(strings.ToLower(client.TrafficLimitType), result)
}

// cycleTrafficMetricNames are the two series the projection reads — and the only ones it
// reads. They are the agent's CycleUp/CycleDown recorded as a gauge: the cycle-to-date
// total the plan limit is written against. Deliberately **not** `net.total.*`, which is
// the kernel counter since boot; see the comment on cycleTrafficSamples.
//
// Both the query and the reader go through this slice, so there is one place to change
// and one place to check.
var cycleTrafficMetricNames = []string{metricstore.MetricTrafficUp, metricstore.MetricTrafficDown}

// cycleSamplesFromSeries turns the queried series into the projection's samples.
//
// A missing direction is unknown, not zero. For a combined limit, refusing the
// entire incomplete history also avoids masking a reset inside a skipped bucket.
// Checking each direction before combining prevents one direction's growth from
// concealing the other's decrease. Bucket maxima can still hide intra-bucket resets.
func cycleSamplesFromSeries(limitType string, result metric.BatchSeriesResult) ([]forecast.Sample, error) {
	needUp, needDown := limitType != "down", limitType != "up"
	type bucketValues struct {
		up, down       int64
		hasUp, hasDown bool
	}
	buckets := make(map[time.Time]bucketValues)
	for index, name := range cycleTrafficMetricNames {
		if (index == 0 && !needUp) || (index == 1 && !needDown) {
			continue
		}
		for _, point := range result.Values[name][metric.AggMax] {
			if math.IsNaN(point.Value) || math.IsInf(point.Value, 0) || point.Value < 0 || point.Value >= math.Ldexp(1, 63) {
				return nil, fmt.Errorf("invalid traffic counter in %s", name)
			}
			at := point.Bucket.UTC()
			values := buckets[at]
			if index == 0 {
				if values.hasUp {
					return nil, fmt.Errorf("duplicate traffic counter bucket in %s", name)
				}
				values.up, values.hasUp = int64(point.Value), true
			} else {
				if values.hasDown {
					return nil, fmt.Errorf("duplicate traffic counter bucket in %s", name)
				}
				values.down, values.hasDown = int64(point.Value), true
			}
			buckets[at] = values
		}
	}

	times := make([]time.Time, 0, len(buckets))
	for at := range buckets {
		times = append(times, at)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	samples := make([]forecast.Sample, 0, len(times))
	var previous bucketValues
	for _, at := range times {
		values := buckets[at]
		if (needUp && !values.hasUp) || (needDown && !values.hasDown) {
			return nil, fmt.Errorf("traffic counter direction missing at %s", at.Format(time.RFC3339Nano))
		}
		if (previous.hasUp && values.hasUp && values.up < previous.up) ||
			(previous.hasDown && values.hasDown && values.down < previous.down) {
			return nil, fmt.Errorf("traffic counter direction decreased at %s; the counter may have restarted", at.Format(time.RFC3339Nano))
		}
		if limitType == "sum" && values.up > math.MaxInt64-values.down {
			return nil, fmt.Errorf("traffic counter sum overflow at %s", at.Format(time.RFC3339Nano))
		}
		samples = append(samples, forecast.Sample{At: at, Total: combineByLimitType(limitType, values.up, values.down)})
		previous = values
	}
	return samples, nil
}

// combineByLimitType mirrors `computeUsedByType` in traffic.go, so the projected quantity is the one the
// limit is defined against.
//
// The default is **`max`**, not `sum`, and that is worth stating because I first wrote `sum` while
// claiming in a comment to mirror the existing function. `computeUsedByType` reaches its `max` branch
// through `fallthrough` from `case "max"`, so an unrecognised or unset type means "whichever direction is
// larger". A forecast that summed instead would warn a `max` node against a number its plan does not
// measure — the sum of both directions can be twice the quantity the limit applies to, so such a node
// would be warned at roughly half its real usage. The test asserts the two functions agree for every type
// rather than only for the ones I thought of.
func combineByLimitType(limitType string, up, down int64) int64 {
	switch limitType {
	case "up":
		return up
	case "down":
		return down
	case "sum":
		return up + down
	case "min":
		if up < down {
			return up
		}
		return down
	default: // "max", and anything unrecognised, matching computeUsedByType's fallthrough.
		if up > down {
			return up
		}
		return down
	}
}

// sampleInterval picks a bucket width for the cycle, targeting enough points for a rate without reading
// more rows than the projection can use.
//
// At least an hour and at most a day, sized so a cycle yields roughly a hundred points. The floor matters
// more than the ceiling: the panel keeps minute-resolution data for only a short window (see
// metric.RawRetention), so asking for a finer interval late in a month would return the handful of minutes
// still held rather than the days the projection needs.
func sampleInterval(cycleLength time.Duration) time.Duration {
	const targetPoints = 100
	interval := cycleLength / targetPoints
	switch {
	case interval < time.Hour:
		return time.Hour
	case interval > 24*time.Hour:
		return 24 * time.Hour
	default:
		// Rounded up to the hour, so the bucket boundaries land on the hour rather than drifting with the
		// cycle's offset.
		return interval.Round(time.Hour)
	}
}
