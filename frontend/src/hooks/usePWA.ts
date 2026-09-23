import { useState, useEffect, useSyncExternalStore } from 'react';

interface PWAState {
  isInstalled: boolean;
  isStandalone: boolean;
  canInstall: boolean;
  isOnline: boolean;
}

const DISPLAY_MODE_QUERY = '(display-mode: standalone)';

const subscribeToDisplayMode = (onStoreChange: () => void) => {
  const mql = window.matchMedia(DISPLAY_MODE_QUERY);
  mql.addEventListener('change', onStoreChange);
  return () => mql.removeEventListener('change', onStoreChange);
};

const getStandaloneSnapshot = () => window.matchMedia(DISPLAY_MODE_QUERY).matches;

const subscribeToConnectivity = (onStoreChange: () => void) => {
  window.addEventListener('online', onStoreChange);
  window.addEventListener('offline', onStoreChange);
  return () => {
    window.removeEventListener('online', onStoreChange);
    window.removeEventListener('offline', onStoreChange);
  };
};

const getOnlineSnapshot = () => navigator.onLine;

export const usePWA = (): PWAState => {
  // Standalone mode and connectivity are browser-owned stores: read them during
  // render and subscribe for changes instead of copying them into state.
  const isStandalone = useSyncExternalStore(subscribeToDisplayMode, getStandaloneSnapshot);
  const isOnline = useSyncExternalStore(subscribeToConnectivity, getOnlineSnapshot);

  // Only the install events need local state, and they are the only writers.
  const [canInstall, setCanInstall] = useState(false);
  const [installed, setInstalled] = useState(false);

  useEffect(() => {
    // Listen for install prompt
    const handleBeforeInstallPrompt = () => {
      setCanInstall(true);
    };

    // Listen for app installed
    const handleAppInstalled = () => {
      setInstalled(true);
      setCanInstall(false);
    };

    window.addEventListener('beforeinstallprompt', handleBeforeInstallPrompt);
    window.addEventListener('appinstalled', handleAppInstalled);

    return () => {
      window.removeEventListener('beforeinstallprompt', handleBeforeInstallPrompt);
      window.removeEventListener('appinstalled', handleAppInstalled);
    };
  }, []);

  // Check if app is installed (rough check)
  const isInstalled =
    installed ||
    isStandalone ||
    (window.navigator as any).standalone ||
    document.referrer.includes('android-app://');

  return { isInstalled, isStandalone, canInstall, isOnline };
};
