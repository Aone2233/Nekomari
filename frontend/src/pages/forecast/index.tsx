import React, { useCallback, useEffect, useState } from "react";
import {
  TrafficForecastPage,
  type ForecastCycle,
  type ForecastRow,
} from "@/components/forecast/TrafficForecastPage";
import { useRPC2Call } from "@/contexts/RPC2Context";

/**
 * The traffic forecast page shell (roadmap H5).
 *
 * Owns the reads and writes; the component owns the presentation and takes them as props, which is what
 * lets the fixture put a node projected to exceed its limit, a node with too little data and a change of
 * cycle day on screen without a server.
 *
 * The preview is a separate call from the save so an operator can see what a different cycle day would do
 * before storing it: the cycle day changes every number on the page, and the agent's own value is not
 * visible from here.
 */
const ForecastShell: React.FC = () => {
  const { call } = useRPC2Call();
  const [rows, setRows] = useState<ForecastRow[]>([]);
  const [cycle, setCycle] = useState<ForecastCycle>({ start: "", end: "", reset_day: 1, location: "" });
  const [threshold, setThreshold] = useState(0.9);
  const [loading, setLoading] = useState(true);

  const load = useCallback(
    async (resetDay?: number) => {
      try {
        const result = await call<
          { reset_day?: number },
          { rows: ForecastRow[]; cycle: ForecastCycle; threshold: number }
        >("admin:getTrafficForecast", resetDay ? { reset_day: resetDay } : {});
        setRows(result?.rows ?? []);
        if (result?.cycle) setCycle(result.cycle);
        if (typeof result?.threshold === "number") setThreshold(result.threshold);
      } finally {
        setLoading(false);
      }
    },
    [call],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const onPreview = useCallback((day: number) => load(day), [load]);

  const onSetCycleDay = useCallback(
    async (day: number) => {
      await call("admin:setTrafficCycleDay", { reset_day: day });
      await load(day);
    },
    [call, load],
  );

  return (
    <TrafficForecastPage
      rows={rows}
      cycle={cycle}
      threshold={threshold}
      onPreview={onPreview}
      onSetCycleDay={onSetCycleDay}
      loading={loading}
    />
  );
};

export default ForecastShell;
export { ForecastShell };