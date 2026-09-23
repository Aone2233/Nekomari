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

const CACHE_KEY = "nekomari.github.releases.v2";

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

function readCache(): CacheEntry | null {
  const store = storage();
  if (!store) return null;
  try {
    const raw = store.getItem(CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as CacheEntry;
    if (!parsed || typeof parsed.attemptedAt !== "number") return null;
    if (parsed.releases !== null && !Array.isArray(parsed.releases)) return null;
    return parsed;
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

async function fetchReleases(): Promise<GithubReleaseInfo[]> {
  const response = await fetch(RELEASES_URL, {
    headers: { Accept: "application/vnd.github+json" },
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
 * clear its state.
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
  const cached = readCache();
  if (cached) {
    const age = now - cached.attemptedAt;
    if (cached.releases && age < RELEASE_CACHE_TTL_MS) return cached.releases;
    if (!cached.releases && age < RELEASE_FAILURE_TTL_MS) {
      throw new Error("the release list could not be fetched recently");
    }
  }

  try {
    const releases = await fetchReleases();
    writeCache({ attemptedAt: now, releases });
    return releases;
  } catch (error) {
    // Record the attempt either way, so a failing network is not retried on
    // every page load.
    writeCache({ attemptedAt: now, releases: cached?.releases ?? null });
    if (cached?.releases) return cached.releases;
    throw error;
  }
}
