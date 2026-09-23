import type { NodeBasicInfo } from "@/contexts/NodeListContext";

export const DAY_MS = 24 * 3600 * 1000;
const EXPIRING_SOON_DAYS = 7;

export function daysUntilExpiry(expiredAt: string, now: number): number {
  return Math.ceil((new Date(expiredAt).getTime() - now) / DAY_MS);
}

export function getExpiringNodes(
  nodeList: readonly NodeBasicInfo[] | null,
  renewedUuids: ReadonlySet<string>,
  now: number,
): NodeBasicInfo[] {
  const deadline = now + EXPIRING_SOON_DAYS * DAY_MS;
  return (nodeList ?? [])
    .filter((node) => {
      if (!node.expired_at || renewedUuids.has(node.uuid)) return false;
      const expiry = new Date(node.expired_at).getTime();
      return expiry >= now && expiry <= deadline;
    })
    .sort(
      (a, b) =>
        new Date(a.expired_at).getTime() - new Date(b.expired_at).getTime(),
    );
}
