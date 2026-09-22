// Keep only the latest pointer position per paint. Flush before ending a drag
// so the final movement is not discarded when the dragging flag is cleared.
export function frameThrottle<T extends unknown[]>(callback: (...args: T) => void) {
  let frame: number | undefined;
  let pending: T | undefined;
  const invoke = () => {
    frame = undefined;
    const args = pending;
    pending = undefined;
    if (args) callback(...args);
  };
  const cancel = () => {
    if (frame !== undefined) cancelAnimationFrame(frame);
    frame = undefined;
    pending = undefined;
  };
  const schedule = (...args: T) => {
    pending = args;
    if (frame === undefined) frame = requestAnimationFrame(invoke);
  };
  schedule.cancel = cancel;
  schedule.flush = () => {
    if (frame !== undefined) cancelAnimationFrame(frame);
    invoke();
  };
  return schedule;
}
