# Traffic forecast direction integrity

The forecast reads the cycle-to-date `traffic.up` and `traffic.down` metrics,
using the maximum in each selected time bucket. A missing observation is not
zero. Earlier versions combined the union of direction buckets and silently
substituted zero for the missing side. That could invent a rate or a reset. For
`sum`, `min` and `max`, growth in one direction could also hide a decrease in
the other when the combined totals remained monotonic.

The fix rejects the whole forecast for a node when a required bucket is
missing, duplicated, negative, non-finite or out of the signed 64-bit range;
it also rejects a sum overflow or a decrease in either required direction.
The admin forecast row reports `insufficient` with the reason; the forecast
notification is skipped and the failure is logged. `up` and `down` plans use
only the selected direction. A present zero is still a valid reading. Existing
storage, retention, agent reports, live used-percentage alerts and credentials
are not changed.

This is deliberately conservative: one missing bucket anywhere in the
selected cycle can suppress the forecast until it ages out or the data is
repaired. It does not prove counter continuity within a bucket: using maxima
can conceal a reset followed by recovery in the same bucket. It also does not
prove the two directions were measured at precisely the same instant within a
bucket. `UsedBytes` is unavailable when the input is refused; the zero in such
a row must not be treated as observed usage. The additive `used_bytes_known`
field distinguishes unavailable from measured usage; the in-panel table
renders unavailable usage as a dash. Older clients that ignore this field must
not interpret a refused row's default zero as a measurement.

Focused Go tests cover complete and one-way series, real store queries,
missing/zero values, masked resets and numerical bounds. The isolated installed
Panel smoke uses synthetic scratch data to check the authenticated forecast
RPC for complete, missing, reset and one-way scenarios. These checks are not a
claim of real fleet faults, a measured production incident, or complete v1.8
forecast semantics.
