package notifier

import (
	"context"
	"fmt"
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

// cycleTrafficSamples reads the node's cumulative traffic over its cycle as a time series.
//
// From the metric store rather than from the latest report, because a projection needs a series: the
// report carries one cumulative total, and one number cannot produce a rate. `net.total.up` and
// `net.total.down` are what the agent's counter is recorded as.
//
// The two are combined per bucket according to the node's limit type, so the series the projection sees
// is the same quantity the limit is checked against — summing them for a `sum` node, taking up for an
// `up` node, and so on. Getting that wrong would project a number the limit does not apply to.
func cycleTrafficSamples(now time.Time, client models.Client, window cycle.Cycle) ([]forecast.Sample, error) {
	store := metricstore.GetStore()
	if store == nil {
		return nil, fmt.Errorf("metric store is not available")
	}

	interval := sampleInterval(window.Duration())
	result, err := store.SeriesBatch(context.Background(), metric.BatchSeriesQuery{
		Specs: []metric.BatchSeriesSpec{
			{MetricName: "net.total.up", Aggregations: []metric.Aggregation{metric.AggMax}, Interval: interval},
			{MetricName: "net.total.down", Aggregations: []metric.Aggregation{metric.AggMax}, Interval: interval},
		},
		EntityIDs: []string{client.UUID},
		Start:     window.Start,
		End:       now.UTC(),
	}, now.UTC())
	if err != nil {
		return nil, err
	}

	limitType := strings.ToLower(client.TrafficLimitType)
	ups := pointsByBucket(result, "net.total.up")
	downs := pointsByBucket(result, "net.total.down")

	// The union of both series' buckets, so a bucket present in only one of them still contributes.
	// Taking the intersection instead would silently drop the first intervals of a node whose up counter
	// and down counter are written in different batches, and a dropped prefix is a rate measured over a
	// window that starts later than the caller believes.
	buckets := make([]time.Time, 0, len(ups)+len(downs))
	seen := make(map[int64]bool, len(ups)+len(downs))
	for _, at := range append(append([]time.Time{}, ups...), downs...) {
		if seen[at.Unix()] {
			continue
		}
		seen[at.Unix()] = true
		buckets = append(buckets, at)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Before(buckets[j]) })

	samples := make([]forecast.Sample, 0, len(buckets))
	for _, at := range buckets {
		up := valueAt(result, "net.total.up", at)
		down := valueAt(result, "net.total.down", at)
		total := combineByLimitType(limitType, int64(up), int64(down))
		if total < 0 {
			// A negative combined value means the counters are not comparable at this instant (one has
			// been recorded and the other has not), so the bucket is not evidence about the cycle total.
			continue
		}
		samples = append(samples, forecast.Sample{At: at, Total: total})
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

// pointsByBucket returns the bucket instants a metric has values for.
func pointsByBucket(result metric.BatchSeriesResult, metricName string) []time.Time {
	byAggregation, ok := result.Values[metricName]
	if !ok {
		return nil
	}
	points, ok := byAggregation[metric.AggMax]
	if !ok {
		return nil
	}
	out := make([]time.Time, 0, len(points))
	for _, point := range points {
		out = append(out, point.Bucket.UTC())
	}
	return out
}

// valueAt reads one metric's value at a bucket instant, or 0 when it has none.
func valueAt(result metric.BatchSeriesResult, metricName string, at time.Time) float64 {
	byAggregation, ok := result.Values[metricName]
	if !ok {
		return 0
	}
	points, ok := byAggregation[metric.AggMax]
	if !ok {
		return 0
	}
	for _, point := range points {
		if point.Bucket.UTC().Equal(at) {
			return point.Value
		}
	}
	return 0
}
