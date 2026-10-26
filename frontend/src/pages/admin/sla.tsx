import { useCallback } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { windowFromLocation } from "@/components/status/slaFormat";
import { SlaReportPage } from "@/pages/sla";
import type { SlaWindow } from "@/types/Sla";

const AdminSlaPage = () => {
  const { pathname, search, hash } = useLocation();
  const navigate = useNavigate();
  const selectedWindow = windowFromLocation(search);
  const onWindowChange = useCallback((nextWindow: SlaWindow) => {
    const params = new URLSearchParams(search);
    params.set("window", nextWindow);
    navigate({ pathname, search: `?${params}`, hash }, { replace: true });
  }, [pathname, search, hash, navigate]);

  // Keep router history intact; remount on window changes to cancel stale pages.
  return <SlaReportPage key={selectedWindow} initialWindow={selectedWindow} onWindowChange={onWindowChange} />;
};

export default AdminSlaPage;
