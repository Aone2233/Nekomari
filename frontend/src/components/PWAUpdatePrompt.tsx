import { useEffect, useState } from "react";
import { Button, Flex, Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { useRegisterSW } from "virtual:pwa-register/react";

/**
 * PWA update prompt (roadmap B1 counterpart).
 *
 * The service worker used to install updates silently (`registerType: "autoUpdate"`): a new
 * deployment activated itself, cleared the old precache, and an already-open tab that lazily
 * imported a route could request a chunk that no longer existed. The user saw a blank page and had
 * to reload by hand. `registerType: "prompt"` makes the new worker wait; this banner is how the
 * user lets it take over, and reloading is the same click.
 *
 * A waiting worker is also why the banner must be visible and reachable: if it never showed,
 * updates would never apply. It therefore renders outside the Radix portal tree, with
 * `role="status"` and an explicit test id.
 */
const UPDATE_CHECK_INTERVAL_MS = 60 * 60 * 1000;

export const PWAUpdatePrompt = () => {
  const { t } = useTranslation();
  const [registration, setRegistration] = useState<ServiceWorkerRegistration | null>(null);
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    updateServiceWorker,
  } = useRegisterSW({
    onRegisteredSW(_scriptUrl, registered) {
      setRegistration(registered ?? null);
    },
  });

  // Browsers normally look for a new worker on navigation; a tab that stays open for days should
  // still notice a release, but checking more often than hourly is noise for a panel.
  useEffect(() => {
    if (!registration) return;
    const timer = window.setInterval(() => {
      void registration.update();
    }, UPDATE_CHECK_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [registration]);

  if (!needRefresh) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="pwa-update-prompt"
      className="fixed bottom-4 right-4 z-[1000] w-[min(22rem,calc(100vw-2rem))] rounded-lg border border-[var(--gray-6)] bg-[var(--color-panel-solid)] p-4 shadow-lg"
    >
      <Flex direction="column" gap="2">
        <Text size="2" weight="bold">
          {t("pwa.update_prompt_title")}
        </Text>
        <Text size="1" color="gray">
          {t("pwa.update_prompt_desc")}
        </Text>
        <Flex gap="2" justify="end">
          <Button
            size="1"
            variant="soft"
            color="gray"
            onClick={() => setNeedRefresh(false)}
          >
            {t("pwa.later")}
          </Button>
          <Button size="1" onClick={() => void updateServiceWorker(true)}>
            {t("pwa.update_now")}
          </Button>
        </Flex>
      </Flex>
    </div>
  );
};
