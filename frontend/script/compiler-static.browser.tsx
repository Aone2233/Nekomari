import { useState } from "react";
import { createRoot } from "react-dom/client";
import i18next from "i18next";
import { I18nextProvider } from "react-i18next";
import PriceTags from "../src/components/PriceTags";

const start = Date.UTC(2026, 8, 23, 12);
const expiry = start + 53 * 24 * 60 * 60 * 1000;
const i18n = i18next.createInstance();
await i18n.init({
  lng: "en",
  resources: { en: { translation: {
    "common.expired_in": "Expires in {{days}} days",
    "common.expired": "Expired",
  } } },
});

export function Fixture() {
  const [price, setPrice] = useState(0);
  return (
    <I18nextProvider i18n={i18n}>
      <button type="button" onClick={() => setPrice(10)}>Set paid</button>
      <div id="price-tags"><PriceTags price={price} expired_at={expiry} /></div>
    </I18nextProvider>
  );
}

createRoot(document.getElementById("root")!).render(<Fixture />);
