/**
 * Reject a client mutation unless the server said it succeeded.
 *
 * The panel's write endpoints answer with a `{status, message}` envelope, and an
 * HTTP 200 carrying `status: "error"` is a failure. Both halves are checked here
 * rather than at each call site, because a caller that only tested `response.ok`
 * would report a rejected mutation as saved.
 *
 * It lived in `pages/admin/index.tsx` until the node table and its dialogs were
 * split out; the callers are in several of those files now.
 */
export async function requireClientMutationSuccess(
  response: Response
): Promise<void> {
  const payload: unknown = await response.json().catch(() => null);
  if (
    response.ok &&
    payload !== null &&
    typeof payload === "object" &&
    "status" in payload &&
    payload.status === "success"
  ) {
    return;
  }
  const message =
    payload !== null &&
    typeof payload === "object" &&
    "message" in payload &&
    typeof payload.message === "string" &&
    payload.message.trim()
      ? payload.message
      : response.ok
        ? "Invalid client mutation response"
        : `HTTP ${response.status}`;
  throw new Error(message);
}
