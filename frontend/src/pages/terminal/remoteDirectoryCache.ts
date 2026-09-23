/**
 * Which directories this tree has already listed.
 *
 * `RemoteFileTree` keeps the listings themselves in `children` state; the only
 * thing it needs on top of that is "have I listed this path since the last
 * reset", so `loadDirectory` can skip a re-list the user did not ask for.
 *
 * That memo lives here, outside React, keyed by the file service it belongs to.
 * It deliberately is *not* a ref: the context-menu builder is reached from
 * render (`buildTreeContextMenuItems(contextTarget)` in the returned JSX), so
 * anything that builder transitively closes over is reachable from render, and
 * `react-hooks/refs` rejects a ref read on that path even though the callback
 * itself only ever runs from an event handler.
 *
 * A `WeakMap` keyed by the service keeps two trees (or the same tree on two
 * nodes) from sharing a memo, and lets the entry go away with the service.
 * `useRemoteFileService` memoises its result, so the identity is stable across
 * renders and only changes when the node uuid or the RPC client does — which is
 * exactly when the tree's listings have to be thrown away.
 */

const listedDirectories = new WeakMap<object, Set<string>>();

/** Has `path` already been listed for this service? */
export const isDirectoryListed = (service: object, path: string): boolean =>
  listedDirectories.get(service)?.has(path) ?? false;

/** Record that `path` has been listed for this service. */
export const markDirectoryListed = (service: object, path: string): void => {
  const listed = listedDirectories.get(service);
  if (listed) {
    listed.add(path);
  } else {
    listedDirectories.set(service, new Set([path]));
  }
};

/**
 * Forget every listing for this service. Called from the tree's reset effect
 * (render may not write external state) whenever the reset triggers fire.
 */
export const clearListedDirectories = (service: object): void => {
  listedDirectories.delete(service);
};
