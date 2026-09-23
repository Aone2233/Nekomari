import { useState } from "react";
import type { ContextMenuPosition } from "./terminalTypes";

export function useContextMenu() {
  const [contextMenuPosition, setContextMenuPosition] = useState<ContextMenuPosition | null>(null);
  const [contextMenuOpen, setContextMenuOpen] = useState(false);

  return {
    contextMenuPosition,
    contextMenuOpen,
    // `buildItems` runs inside the animation frame that opens the menu, not
    // during render: a caller whose item list is expensive, or whose items
    // close over a ref, must not build them for a menu that stays shut.
    openContextMenu: (
      event: { clientX: number; clientY: number; preventDefault?: () => void },
      buildItems?: () => void,
    ) => {
      event.preventDefault?.();
      setContextMenuPosition({ x: event.clientX, y: event.clientY });
      requestAnimationFrame(() => {
        buildItems?.();
        setContextMenuOpen(true);
      });
    },
    closeContextMenu: () => setContextMenuOpen(false),
    setContextMenuOpen,
  };
}
