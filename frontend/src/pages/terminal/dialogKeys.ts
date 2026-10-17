/**
 * TerminalDialog 的键盘判定，抽成纯函数是为了可测。
 *
 * 这段逻辑原来内联在全屏 backdrop 的 onKeyDown 里，而它有一个会删文件的 bug：
 * backdrop 把 `Enter` 一律当作「确认」，但「取消」按钮是它的子节点 —— 回车激活
 * 聚焦的按钮是那次 keydown 的**默认行为**，冒泡阶段的 `preventDefault()` 会把它
 * 抑制掉，于是焦点停在「取消」上按回车反而执行了确认。这个对话框被 `destructive`
 * 用于删除文件（FileManagerPanel / FileEditorDialog），所以那是一次数据丢失。
 *
 * 判定本身与 React 无关，放在这里就能被 `npm test` 直接覆盖，而不是只能靠一个
 * 需要真实浏览器的 fixture。
 */

/** 事件里我们真正关心的部分。`target` 取 `unknown`，这样 React 的
 * `KeyboardEvent`（其 `target` 是 `EventTarget`）可以直接传进来，而判定仍在函数里收窄。 */
export type EnterKeyEvent = {
  key: string;
  target?: unknown;
};

/**
 * 这次按键是否应当被当作「确认」。
 *
 * 规则：必须是 Enter、不能正在提交、而且事件不能来自一个 `<button>`。
 * 第三条是修复本身：来自按钮的 Enter 属于那个按钮，`preventDefault()` 会吃掉
 * 它自己的点击。
 */
export function shouldSubmitOnEnter(
  event: EnterKeyEvent,
  options: { submitting: boolean },
): boolean {
  if (event.key !== "Enter") return false;
  if (options.submitting) return false;
  const target = event.target as { tagName?: unknown } | null | undefined;
  const tag = typeof target?.tagName === "string" ? target.tagName : "";
  return tag.toUpperCase() !== "BUTTON";
}
