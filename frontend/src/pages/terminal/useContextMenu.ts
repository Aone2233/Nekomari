import { useState } from "react";
import type { ContextMenuPosition } from "./terminalTypes";

export function useContextMenu() {
  const [contextMenuPosition, setContextMenuPosition] = useState<ContextMenuPosition | null>(null);
  const [contextMenuOpen, setContextMenuOpen] = useState(false);

  return {
    contextMenuPosition,
    contextMenuOpen,
    openContextMenu: (event: { clientX: number; clientY: number; preventDefault?: () => void }) => {
      event.preventDefault?.();
      setContextMenuPosition({ x: event.clientX, y: event.clientY });
      requestAnimationFrame(() => setContextMenuOpen(true));
    },
    closeContextMenu: () => setContextMenuOpen(false),
    setContextMenuOpen,
  };
}
