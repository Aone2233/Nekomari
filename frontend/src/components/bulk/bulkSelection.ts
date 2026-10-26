/**
 * How a node selection travels from the admin list into the in-panel bulk page.
 *
 *   - the query string keeps selection usable after a reload or in a copied link;
 *   - the builder and the parser have to agree on the parameter, including how it is encoded. They
 *     live together so the two sides cannot drift into a link that opens a page with an empty
 *     selection — which looks like the wiring working and being ignored.
 */

/** The panel-owned route; the old standalone document remains available. */
export const BULK_EDIT_PATH = "/admin/bulk";

/** The query parameter that carries the checked node uuids. */
export const BULK_SELECTION_PARAM = "uuids";

/**
 * bulkEditHref is the link the admin list opens, with the checked uuids in the query.
 *
 * uuids are encoded one by one rather than through URLSearchParams as a whole: that keeps the
 * comma separator literal (`?uuids=uuid-a,uuid-b`) instead of turning the link into
 * `?uuids=uuid-a%2Cuuid-b`, and `selectionFromSearch` decodes either spelling.
 */
export const bulkEditHref = (uuids: string[]): string => {
  const unique = [...new Set(uuids.map((uuid) => uuid.trim()).filter(Boolean))];
  if (unique.length === 0) return BULK_EDIT_PATH;
  const encoded = unique.map((uuid) => encodeURIComponent(uuid)).join(",");
  return `${BULK_EDIT_PATH}?${BULK_SELECTION_PARAM}=${encoded}`;
};

/**
 * selectionFromSearch reads the uuids out of a location search.
 *
 * Trimmed, de-duplicated and in the order the admin list selected them, because the page shows
 * the selection as rows: duplicates would double-count and a different order would make the
 * initial selection look like it had been re-sorted.
 */
export const selectionFromSearch = (search: string): string[] => {
  const raw = new URLSearchParams(search).get(BULK_SELECTION_PARAM) ?? "";
  return [...new Set(raw.split(",").map((uuid) => uuid.trim()).filter(Boolean))];
};
