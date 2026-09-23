/**
 * Mounted fixture for `components/ui/number-picker` and its only call site,
 * `pages/admin/log`.
 *
 * The picker half tracks how often `onChange` fires so a spurious echo on an
 * unrelated parent render is visible as a count, not as a guess. The log half
 * mounts the real page against a stubbed `/api/admin/logs` so pagination can be
 * driven for real.
 */
import { useCallback, useState } from "react";
import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import { Theme } from "@radix-ui/themes";
import { MemoryRouter } from "react-router-dom";
import NumberPicker from "../src/components/ui/number-picker";
import LogPage from "../src/pages/admin/log";

declare global {
  interface Window {
    numberPickerFixture: {
      setDefaultValue: (value: number) => void;
      rerender: () => void;
      resetCount: () => void;
      requests: { limit: number; page: number }[];
    };
  }
}

const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: {
    en: {
      translation: {
        "logs.title": "Logs",
        "pprof.title": "pprof",
        "log.title": "Log entry",
        "common.close": "Close",
        "common.previous_page": "Previous page",
        "common.next_page": "Next page",
      },
    },
  },
});

// The log page reads the API through the global fetch; the fixture answers it
// locally so the page's own request/page pairing is observable.
const requests: { limit: number; page: number }[] = [];
window.numberPickerFixture = {
  setDefaultValue: () => {},
  rerender: () => {},
  resetCount: () => {},
  requests,
};

window.fetch = (async (input: RequestInfo | URL) => {
  const url = new URL(String(input), window.location.origin);
  const limit = Number(url.searchParams.get("limit"));
  const page = Number(url.searchParams.get("page"));
  requests.push({ limit, page });
  const logs = Array.from({ length: limit }, (_, index) => {
    const id = (page - 1) * limit + index + 1;
    return {
      id,
      ip: `10.0.0.${id % 250}`,
      uuid: `uuid-${id}`,
      message: `message-${id}`,
      msg_type: "info",
      time: "2026-09-23T12:00:00Z",
    };
  });
  return new Response(JSON.stringify({ data: { logs, total: 50 } }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}) as typeof fetch;

function PickerSection() {
  const [defaultValue, setDefaultValue] = useState(7);
  const [calls, setCalls] = useState<number[]>([]);
  const [rerenders, setRerenders] = useState(0);
  const [stableCalls, setStableCalls] = useState<number[]>([]);

  // A memoised handler is what the picker's contract asks for: it must not
  // change identity just because the parent re-rendered.
  const onStableChange = useCallback((value: number) => {
    setStableCalls((previous) => [...previous, value]);
  }, []);

  window.numberPickerFixture.setDefaultValue = setDefaultValue;
  window.numberPickerFixture.rerender = () =>
    setRerenders((count) => count + 1);
  window.numberPickerFixture.resetCount = () => {
    setCalls([]);
    setStableCalls([]);
  };

  return (
    <section id="picker">
      <output id="default-value">{defaultValue}</output>
      <output id="rerenders">{rerenders}</output>
      <output id="calls">{calls.join(",")}</output>
      <output id="call-count">{calls.length}</output>
      <output id="stable-calls">{stableCalls.join(",")}</output>
      <output id="stable-count">{stableCalls.length}</output>
      <div id="inline-picker">
        {/* Deliberately the shape log.tsx used to pass: a fresh arrow on every
            render. The picker must not re-fire because of it. */}
        <NumberPicker
          defaultValue={defaultValue}
          onChange={(value) => setCalls((previous) => [...previous, value])}
          min={1}
          max={20}
        />
      </div>
      <div id="stable-picker">
        <NumberPicker
          defaultValue={defaultValue}
          onChange={onStableChange}
          min={1}
          max={20}
        />
      </div>
    </section>
  );
}

function App() {
  return (
    <I18nextProvider i18n={i18n}>
      <Theme>
        <PickerSection />
        <section id="log">
          <MemoryRouter>
            <LogPage />
          </MemoryRouter>
        </section>
      </Theme>
    </I18nextProvider>
  );
}

export { App };

createRoot(document.getElementById("root")!).render(<App />);
