import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import "./global.css";
import "@radix-ui/themes/styles.css";
import { initI18n } from "./i18n/config";
import ErrorBoundary from "./components/ErrorBoundary";
import { App } from "./App";

// 首帧要等当前语言：先 render 再等语言包到，屏幕上会先出现 key、再整片替换成译文。
// 用 `void …then(render)` 而不是顶层 await——入口是经典的浏览器脚本，不走 ESM 的顶层
// await 语义，Vite 的 legacy 目标也不接受它。
void initI18n().then(() => {
  createRoot(document.getElementById("root")!).render(
    <ErrorBoundary>
      <StrictMode>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </StrictMode>
    </ErrorBoundary>,
  );
});
