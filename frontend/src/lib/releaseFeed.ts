/**
 * The repository's published releases, with a browser-side cache.
 *
 * The admin bar shows an "update available" indicator, and it used to work that
 * out by fetching GitHub's release list on every mount — which is every admin
 * page navigation — with `cache: "no-cache"` to defeat the HTTP cache as well.
 * Unauthenticated GitHub API requests are limited to 60 per hour per IP, so
 * ordinary browsing could exhaust that budget and turn the indicator into a
 * console error; behind a blocked or rate-limited network it always did.
 *
 * Both outcomes are cached, and that is the point rather than a detail. Caching
 * only successes fixes the happy path and leaves the broken one worse than
 * before: where GitHub is unreachable the fetch fails, nothing is written, and
 * the next page load tries again — a request per navigation, forever. So a
 * failed attempt is recorded too, with a shorter lifetime, and is served from
 * the cache until it expires.
 *
 * Only the raw release list is cached, never the "is it newer" verdict: a
 * browser that has since updated the panel still compares the same list against
 * its new version and reaches the right answer.
 *
 * The list's age and the last attempt's age are deliberately two fields. One
 * timestamp cannot express both: recording a failed refresh under the same name
 * as a successful fetch made the stale list look freshly fetched, so it was
 * served for another full six hours and the fifteen-minute retry the failure
 * path exists for never happened.
 */

export interface GithubReleaseInfo {
  tag_name: string;
  name?: string;
  body?: string;
  html_url: string;
  published_at?: string;
  draft?: boolean;
  prerelease?: boolean;
}

const RELEASES_URL =
  "https://api.github.com/repos/Aone2233/Nekomari/releases?per_page=100";

const CACHE_KEY = "nekomari.github.releases.v3";

/**
 * The v2 entry carried a single `attemptedAt`, which a failed attempt could
 * have written. Reading it back as a success time would restore exactly the bug
 * v3 fixes, so it is discarded rather than migrated; the cost is one extra
 * GitHub request for browsers that already hold one.
 */
const LEGACY_CACHE_KEYS = ["nekomari.github.releases.v2"];

/**
 * Six hours: long enough that ordinary browsing never re-fetches, short enough
 * that a release published in the morning shows up the same day.
 */
export const RELEASE_CACHE_TTL_MS = 6 * 60 * 60 * 1000;

/**
 * Fifteen minutes for a failed attempt. Long enough that a blocked or
 * rate-limited network does not cost a request per page load, short enough that
 * a transient outage clears itself without the user doing anything.
 */
export const RELEASE_FAILURE_TTL_MS = 15 * 60 * 1000;

type CacheEntry = {
  /** When the last successful fetch finished. Failure never moves this. */
  fetchedAt: number;
  /** When the last attempt finished, successful or not. */
  attemptedAt: number;
  /** The last successfully fetched list, or null if none has ever succeeded. */
  releases: GithubReleaseInfo[] | null;
};

/** The attempt currently in progress, if any, so concurrent callers share it. */
let inFlight: Promise<GithubReleaseInfo[]> | null = null;

function storage(): Storage | null {
  try {
    return typeof window !== "undefined" ? window.localStorage : null;
  } catch {
    // Some privacy modes throw on access rather than returning null.
    return null;
  }
}

function isTimestamp(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function readCache(): CacheEntry | null {
  const store = storage();
  if (!store) return null;
  try {
    const raw = store.getItem(CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<CacheEntry>;
    if (!parsed || typeof parsed !== "object") return null;
    if (!isTimestamp(parsed.attemptedAt)) return null;
    if (parsed.releases !== null && !Array.isArray(parsed.releases)) return null;
    // An entry with no list needs no success time; one with a list does, and a
    // missing or unparsable one means the entry cannot be aged. Fall back to
    // treating the list as never successfully fetched so the next load attempts
    // a refresh rather than serving it forever.
    const fetchedAt = isTimestamp(parsed.fetchedAt) ? parsed.fetchedAt : null;
    if (parsed.releases !== null && fetchedAt === null) return null;
    return {
      fetchedAt: fetchedAt ?? 0,
      attemptedAt: parsed.attemptedAt,
      releases: parsed.releases ?? null,
    };
  } catch {
    return null;
  }
}

function writeCache(entry: CacheEntry): void {
  const store = storage();
  if (!store) return;
  try {
    store.setItem(CACHE_KEY, JSON.stringify(entry));
  } catch {
    // The cache is an optimisation; a full or disabled store must not break the
    // indicator.
  }
}

/** Drop the pre-v3 entries, whose single timestamp is not trustworthy. */
function clearLegacyCache(): void {
  const store = storage();
  if (!store) return;
  for (const key of LEGACY_CACHE_KEYS) {
    try {
      store.removeItem(key);
    } catch {
      // Same reasoning as writeCache: a hostile store is not worth failing over.
    }
  }
}

async function fetchReleases(): Promise<GithubReleaseInfo[]> {
  const response = await fetch(RELEASES_URL, {
    headers: { Accept: "application/vnd.github+json" },
    // The service worker caches `api.*` responses with NetworkFirst, and a
    // cached replay is indistinguishable here from a fresh answer — the caller
    // would stamp it with the current time and the real TTL would never start.
    // The vite.config.ts runtime rule also excludes this host; this is the
    // second, local half of the same guard, and it keeps the module correct if
    // that rule is ever broadened again.
    cache: "no-store",
  });
  if (!response.ok) throw new Error(`GitHub HTTP ${response.status}`);
  const data = (await response.json()) as GithubReleaseInfo[];
  return (data || []).filter(
    (release) => !release.draft && !release.prerelease,
  );
}

/**
 * The published releases, newest first, with drafts and prereleases removed.
 *
 * Serves the cache while it is fresh. On a failed refresh it returns the last
 * successful list if there is one — a stale list still answers "was there a
 * newer release at the last check" — and otherwise rethrows so the caller can
 * clear its state. A failure does not extend the list's freshness: it only
 * postpones the next attempt by the failure window.
 *
 * Concurrent callers share one attempt: the admin bar mounts twice on its first
 * render, and without this both mounts miss the empty cache and fetch.
 */
export async function loadGithubReleases(
  now: number = Date.now(),
): Promise<GithubReleaseInfo[]> {
  if (inFlight) return inFlight;

  const attempt = loadOnce(now);
  inFlight = attempt;
  try {
    return await attempt;
  } finally {
    if (inFlight === attempt) inFlight = null;
  }
}

/** One attempt: cache first, then the network, recording either outcome. */
async function loadOnce(now: number): Promise<GithubReleaseInfo[]> {
  clearLegacyCache();

  const cached = readCache();
  if (cached) {
    // Two windows, two timestamps. A list is fresh for six hours *after it was
    // fetched*; an attempt is not repeated for fifteen minutes *after it
    // failed*, whether or not there is a list to show meanwhile.
    if (cached.releases && now - cached.fetchedAt < RELEASE_CACHE_TTL_MS) {
      return cached.releases;
    }
    if (now - cached.attemptedAt < RELEASE_FAILURE_TTL_MS) {
      if (cached.releases) return cached.releases;
      throw new Error("the release list could not be fetched recently");
    }
  }

  try {
    const releases = await fetchReleases();
    writeCache({ fetchedAt: now, attemptedAt: now, releases });
    return releases;
  } catch (error) {
    // Record the attempt either way, so a failing network is not retried on
    // every page load. The success time is carried over untouched: a failure
    // says nothing about when the list was last known to be current, so a keep
    // entry keeps its age and a failure with nothing to keep stores a null list
    // with its own attempt time.
    writeCache({
      fetchedAt: cached?.releases ? cached.fetchedAt : 0,
      attemptedAt: now,
      releases: cached?.releases ?? null,
    });
    if (cached?.releases) return cached.releases;
    throw error;
  }
}
