import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "../src/components/ui/chart";
import "../src/global.css";

const config: ChartConfig = {
  visits: {
    label: "Visits",
    theme: { light: "#c2410c", dark: "#fdba74" },
  },
  sales: {
    label: "Sales",
    theme: { light: "#0369a1", dark: "#7dd3fc" },
  },
};
const first = [
  { time: "Mon", visits: 8, sales: 3 },
  { time: "Tue", visits: 12, sales: 6 },
  { time: "Wed", visits: 5, sales: 9 },
];
const updated = [
  { time: "Mon", visits: 2, sales: 8 },
  { time: "Tue", visits: 7, sales: 4 },
  { time: "Wed", visits: 14, sales: 3 },
];

export function Fixture() {
  const [dark, setDark] = useState(false);
  const [changed, setChanged] = useState(false);
  const [labelled, setLabelled] = useState(true);
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
  }, [dark]);
  return (
    <main style={{ padding: 24 }}>
      <button type="button" onClick={() => setDark((value) => !value)}>
        Toggle theme
      </button>
      <button type="button" onClick={() => setChanged((value) => !value)}>
        Update series
      </button>
      <ChartContainer
        id="browser-fixture"
        config={config}
        aria-label={labelled ? "Weekly visits and sales" : undefined}
        style={{ width: 500, height: 280 }}
      >
        <LineChart data={changed ? updated : first} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="time" />
          <YAxis />
          <ChartTooltip content={<ChartTooltipContent />} />
          <ChartLegend content={<ChartLegendContent />} />
          <Line
            dataKey="visits"
            stroke="var(--color-visits)"
            isAnimationActive
            animationDuration={180}
          />
          <Line
            dataKey="sales"
            stroke="var(--color-sales)"
            isAnimationActive
            animationDuration={180}
          />
        </LineChart>
      </ChartContainer>
      <button type="button" onClick={() => setLabelled((value) => !value)}>
        Toggle explicit label
      </button>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<Fixture />);
