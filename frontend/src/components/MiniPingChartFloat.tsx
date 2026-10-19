import React, { useState, useRef, useCallback, useEffect } from "react";
import { Popover } from "@radix-ui/themes";
// 图表只在弹层 open 时才渲染（悬停 3 秒或点击），但静态 import 会把 recharts 一起
// 拖进调用方的 chunk，让首访下载一个大多数访客不会打开的功能。lazy 把这条边变成
// 「第一次打开弹层时的一次按需请求」，之后由模块缓存复用。
const MiniPingChart = React.lazy(() => import("./MiniPingChart"));

interface FloatMiniPingChartProps {
  uuid: string;
  trigger: React.ReactNode; 
  chartWidth?: string | number; 
  chartHeight?: string | number;
  hours?: number;
}

const MiniPingChartFloat: React.FC<FloatMiniPingChartProps> = ({
  uuid,
  trigger,
  chartWidth = 400,
  chartHeight = 200,
  hours = 12,
}) => {
  const [open, setOpen] = useState(false);
  const hoverTimeoutRef = useRef<number | null>(null);

  const clearHoverTimeout = useCallback(() => {
    if (hoverTimeoutRef.current) {
      window.clearTimeout(hoverTimeoutRef.current);
      hoverTimeoutRef.current = null;
    }
  }, []);

  useEffect(() => clearHoverTimeout, [clearHoverTimeout]);

  const handleMouseEnter = useCallback(() => {
    clearHoverTimeout();
    hoverTimeoutRef.current = window.setTimeout(() => {
      setOpen(true);
    }, 3000);
  }, [clearHoverTimeout]);

  const handleMouseLeave = useCallback(() => {
    clearHoverTimeout();
    hoverTimeoutRef.current = window.setTimeout(() => {
      setOpen(false);
    }, 200);
  }, [clearHoverTimeout]);

  const handleClick = useCallback(() => {
    clearHoverTimeout();
    setOpen((prev) => !prev);
  }, [clearHoverTimeout]);

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger>
        <span
          onMouseEnter={handleMouseEnter}
          onMouseLeave={handleMouseLeave}
          onClick={handleClick}
          style={{ cursor: "pointer" }}
          className="km-mini-ping-chart-float flex items-center justify-center"
        >
          {trigger}
        </span>
      </Popover.Trigger>
      <Popover.Content
        sideOffset={5}
        collisionPadding={12}
        onMouseEnter={handleMouseEnter}
        onMouseLeave={handleMouseLeave}
        style={{
          padding: 0,
          border: "none",
          width: typeof chartWidth === "number" ? `${chartWidth}px` : chartWidth,
          maxWidth: "calc(100vw - 24px)",
          maxHeight: "calc(100vh - 24px)",
          overflow: "auto",
          boxShadow:
            "hsl(206 22% 7% / 35%) 0px 10px 38px -10px, hsl(206 22% 7% / 20%) 0px 10px 20px -15px",
          borderRadius: "var(--radius-3)",
          zIndex: 5,
        }}
      >
        {open && (
          <React.Suspense fallback={null}>
            <MiniPingChart
              hours={hours}
              uuid={uuid}
              width="100%"
              height={chartHeight}
            />
          </React.Suspense>
        )}
      </Popover.Content>
    </Popover.Root>
  );
};

export default MiniPingChartFloat;
