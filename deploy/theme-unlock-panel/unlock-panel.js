/*
 * Nekomari — 流媒体 / AI 解锁区块
 *
 * 为什么是这样一个附加脚本，而不是改主题
 * --------------------------------------
 * LuminaPlus 1.3.3（在用的）与上游 1.3.4 都没有渲染解锁的代码：全部 chunk 里搜不到
 * 「解锁」「流媒体」「Netflix」，CSS 里也没有对应的 class，主题的 IP 面板只画三个
 * section（地理信息 / 网络信息 / 全球延迟检测）。schema 里的
 * capabilities.media_unlock / ai_unlock 是留的占位，参考插件 Komari-IP-Info 自己也把
 * 它们写死成 false。
 *
 * 所以数据有了也没地方显示，只能补一层。这里刻意【不改主题的压缩产物】：压缩过的
 * React bundle 改起来极易出错，而且主题一升级就失效。附加脚本只依赖主题已经公开的
 * DOM 结构（.ip-info-panel / .ip-info-address）和它自己用的那个接口。
 *
 * 数据从哪来
 * ----------
 * 由节点上的探针测量后上报，服务端存在 unlock_reports 表里，随
 * GET /api/public/ip-info/v1/lookup 的 data.unlock 一起返回。没有探测记录时该字段
 * 不存在，这里显示「等待探测」—— 「还没测」和「未解锁」必须分得开。
 */
(() => {
  'use strict';

  const PANEL_SELECTOR = '.ip-info-panel';
  const BLOCK_CLASS = 'nk-unlock';
  const STYLE_ID = 'nk-unlock-style';

  const STATUS_LABEL = {
    unlocked: '已解锁',
    partial: '部分解锁',
    blocked: '未解锁',
    unknown: '未知',
  };

  const KIND_LABEL = { media: '流媒体', ai: 'AI' };

  const STYLE = `
.nk-unlock-grid { display: grid; gap: 8px; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); }
.nk-unlock-item { border: 1px solid var(--border-color, rgba(128,128,128,.25)); border-radius: 10px; padding: 9px 11px; display: flex; flex-direction: column; gap: 3px; }
.nk-unlock-head { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; }
.nk-unlock-name { font-weight: 600; font-size: 13px; }
.nk-unlock-status { font-size: 12px; font-weight: 600; white-space: nowrap; }
.nk-unlock-detail { font-size: 11.5px; opacity: .68; line-height: 1.45; }
.nk-unlock-item.is-unlocked .nk-unlock-status { color: #16a34a; }
.nk-unlock-item.is-partial  .nk-unlock-status { color: #d97706; }
.nk-unlock-item.is-blocked  .nk-unlock-status { color: #dc2626; }
.nk-unlock-item.is-unknown  .nk-unlock-status { color: #6b7280; }
.nk-unlock-meta { font-size: 11.5px; opacity: .6; line-height: 1.5; }
.nk-unlock-note { font-size: 11.5px; opacity: .75; margin-top: 8px; line-height: 1.5; }
`;

  function ensureStyle() {
    if (document.getElementById(STYLE_ID)) return;
    const style = document.createElement('style');
    style.id = STYLE_ID;
    style.textContent = STYLE;
    document.head.appendChild(style);
  }

  // 面板地址栏里的 uuid 与当前选中的地址。地址会被 IPv4/IPv6 切换改变，所以每次
  // 渲染都重新读一遍，并在切换后重画。
  function currentTarget() {
    const match = location.pathname.match(/\/instance\/([^/?#]+)/);
    if (!match) return null;
    const panel = document.querySelector(PANEL_SELECTOR);
    if (!panel) return null;
    const addressEl = panel.querySelector('.ip-info-address strong');
    const ip = addressEl ? addressEl.textContent.trim() : '';
    if (!ip) return null;
    return { uuid: decodeURIComponent(match[1]), ip };
  }

  function sectionOf(panel) {
    return panel.querySelector('.' + BLOCK_CLASS);
  }

  function buildSection(unlock) {
    const section = document.createElement('section');
    section.className = 'ip-info-section ' + BLOCK_CLASS;

    const heading = document.createElement('div');
    heading.className = 'ip-info-section-heading';
    const title = document.createElement('h3');
    title.textContent = '流媒体 / AI 解锁';
    heading.appendChild(title);
    const meta = document.createElement('span');
    meta.className = 'nk-unlock-meta';
    const parts = [];
    if (unlock.egress_ip) parts.push('出口 ' + unlock.egress_ip);
    if (unlock.egress_region) parts.push(unlock.egress_region);
    if (unlock.probed_at) parts.push('探测于 ' + formatTime(unlock.probed_at));
    meta.textContent = parts.join(' · ');
    heading.appendChild(meta);
    section.appendChild(heading);

    const grid = document.createElement('div');
    grid.className = 'nk-unlock-grid';
    for (const result of unlock.results || []) {
      grid.appendChild(buildItem(result));
    }
    section.appendChild(grid);

    // 出口地址不一定是节点自己的地址 —— 本机群里有主机经由另一台节点出网。不说清楚
    // 的话，这份结论会被算到错误的节点上。
    const note = document.createElement('p');
    note.className = 'nk-unlock-note';
    note.textContent = '由节点上的探针测量。标记为「未知」的服务无法从普通 HTTP 客户端判断：' +
      'ChatGPT 与 Claude 对数据中心出口一律返回 403（风控），与地区封锁无法区分；' +
      '标为「按地区判断」的只验证了出口地区，没有验证该 IP 是否真的可用。';
    section.appendChild(note);

    return section;
  }

  function buildItem(result) {
    const item = document.createElement('div');
    item.className = 'nk-unlock-item is-' + (result.status || 'unknown');

    const head = document.createElement('div');
    head.className = 'nk-unlock-head';
    const name = document.createElement('span');
    name.className = 'nk-unlock-name';
    name.textContent = result.name || result.id;
    const status = document.createElement('span');
    status.className = 'nk-unlock-status';
    status.textContent = STATUS_LABEL[result.status] || result.status;
    head.appendChild(name);
    head.appendChild(status);
    item.appendChild(head);

    const detail = document.createElement('span');
    detail.className = 'nk-unlock-detail';
    const bits = [];
    if (KIND_LABEL[result.kind]) bits.push(KIND_LABEL[result.kind]);
    if (result.region) bits.push(result.region);
    if (result.detail) bits.push(result.detail);
    detail.textContent = bits.join(' · ');
    item.appendChild(detail);

    return item;
  }

  function formatTime(value) {
    const date = new Date(value);
    if (!Number.isFinite(date.getTime())) return value;
    return new Intl.DateTimeFormat('zh-CN', {
      month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
    }).format(date);
  }

  let pending = false;

  async function render() {
    const panel = document.querySelector(PANEL_SELECTOR);
    if (!panel) return;
    const target = currentTarget();
    if (!target) return;

    // 已经画过且还是同一个地址就不重画 —— 这个函数由 MutationObserver 驱动，
    // 不设这道闸会自己触发自己。
    const existing = sectionOf(panel);
    if (existing && existing.dataset.target === target.ip) return;

    let payload;
    try {
      const response = await fetch(
        '/api/public/ip-info/v1/lookup?' +
          new URLSearchParams({ uuid: target.uuid, ip: target.ip }).toString(),
        { credentials: 'include', cache: 'no-store', headers: { Accept: 'application/json' } },
      );
      if (!response.ok) return;
      payload = await response.json();
    } catch (_) {
      return;
    }

    // 切换地址的瞬间可能已经换过一次，回来时确认目标没变。
    const still = currentTarget();
    if (!still || still.ip !== target.ip) return;

    const unlock = payload && payload.data && payload.data.unlock;
    const anchor = panel.querySelector('.ip-info-detail-grid');

    if (!unlock || !unlock.results || !unlock.results.length) {
      // 还没有探测记录：说清楚是「等待探测」，不要留空让人以为没有这个功能。
      if (existing) existing.remove();
      const placeholder = document.createElement('section');
      placeholder.className = 'ip-info-section ' + BLOCK_CLASS;
      placeholder.dataset.target = target.ip;
      const heading = document.createElement('div');
      heading.className = 'ip-info-section-heading';
      const title = document.createElement('h3');
      title.textContent = '流媒体 / AI 解锁';
      heading.appendChild(title);
      placeholder.appendChild(heading);
      const note = document.createElement('p');
      note.className = 'nk-unlock-note';
      note.textContent = '等待节点探针上报（每 6 小时一次，节点刚接入时需等一次探测）。';
      placeholder.appendChild(note);
      if (anchor) anchor.after(placeholder); else panel.appendChild(placeholder);
      return;
    }

    const section = buildSection(unlock);
    section.dataset.target = target.ip;
    if (existing) existing.remove();
    if (anchor) anchor.after(section); else panel.appendChild(section);
  }

  function schedule() {
    if (pending) return;
    pending = true;
    // 面板是异步挂载的，而且切换地址会重建节点；用一帧的延迟合并同一批变更。
    requestAnimationFrame(() => {
      pending = false;
      render();
    });
  }

  function start() {
    ensureStyle();
    new MutationObserver(schedule).observe(document.body, { childList: true, subtree: true });
    // 主题内部路由不会重新加载页面，所以也要跟着 history 变化重画。
    for (const method of ['pushState', 'replaceState']) {
      const original = history[method];
      history[method] = function (...args) {
        const result = original.apply(this, args);
        schedule();
        return result;
      };
    }
    window.addEventListener('popstate', schedule);
    schedule();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
