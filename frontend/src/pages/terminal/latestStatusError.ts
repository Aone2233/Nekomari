/**
 * common:getNodesLatestStatus answers JSON-RPC InvalidParams (-32602) with
 * "Node not found" when the requested node has no report at all: it has never
 * reported, or the client was deleted. The resource monitors render that as an
 * offline node, the way the fleet-wide response used to, instead of surfacing a
 * request failure.
 */
const JSON_RPC_INVALID_PARAMS = -32602;
const NODE_NOT_FOUND_MESSAGE = "Node not found";

export const isNodeNotFoundError = (error: unknown): boolean => {
  const message =
    error instanceof Error
      ? error.message
      : typeof error === "string"
        ? error
        : "";
  if (!message) return false;

  return (
    message.includes(String(JSON_RPC_INVALID_PARAMS)) ||
    message.includes(NODE_NOT_FOUND_MESSAGE)
  );
};
