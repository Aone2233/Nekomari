import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const require = createRequire(import.meta.url);
const sourceFile = fileURLToPath(new URL('../src/pages/terminal/useTerminalPage.ts', import.meta.url));
const source = ts.transpileModule(readFileSync(sourceFile, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

function createHookRunner() {
  const cells = [];
  let cursor = 0;
  let changed = false;
  let pendingEffects = [];
  let nextTabId = 0;
  const t = key => key;
  const settings = { customCss: '', terminalPadding: 0, terminalOptions: { fontFamily: 'monospace' } };
  const unchanged = (left, right) => left && right &&
    left.length === right.length && left.every((value, index) => Object.is(value, right[index]));

  const react = {
    useState(initial) {
      const index = cursor++;
      if (!cells[index]) {
        const cell = { value: typeof initial === 'function' ? initial() : initial };
        cell.set = value => {
          const next = typeof value === 'function' ? value(cell.value) : value;
          if (!Object.is(next, cell.value)) {
            cell.value = next;
            changed = true;
          }
        };
        cells[index] = cell;
      }
      return [cells[index].value, cells[index].set];
    },
    useRef(initial) {
      const index = cursor++;
      return (cells[index] ??= { current: initial });
    },
    useMemo(factory, deps) {
      const index = cursor++;
      const cell = cells[index];
      if (cell && unchanged(cell.deps, deps)) return cell.value;
      const value = factory();
      cells[index] = { value, deps };
      return value;
    },
    useCallback(callback, deps) {
      return react.useMemo(() => callback, deps);
    },
    useEffect(effect, deps) {
      scheduleEffect('passive', effect, deps);
    },
    useLayoutEffect(effect, deps) {
      scheduleEffect('layout', effect, deps);
    },
  };

  function scheduleEffect(kind, effect, deps) {
    const index = cursor++;
    const cell = (cells[index] ??= { kind, deps: undefined, cleanup: undefined });
    if (!unchanged(cell.deps, deps)) pendingEffects.push({ cell, kind, effect, deps });
  }

  const document = {
    title: '',
    head: { appendChild() {} },
    body: { style: {} },
    createElement() { return { remove() {} }; },
    addEventListener() {}, removeEventListener() {},
  };
  const window = {
    innerWidth: 1200,
    location: { protocol: 'https:', search: '' },
    setTimeout() { return 1; }, clearTimeout() {},
    addEventListener() {}, removeEventListener() {},
  };
  const module = { exports: {} };
  vm.runInNewContext(source, {
    module, exports: module.exports, document, window, URLSearchParams,
    fetch: () => new Promise(() => {}),
    require(specifier) {
      if (specifier === 'react') return react;
      if (specifier === 'react-i18next') return { useTranslation: () => ({ t }) };
      if (specifier === 'sonner') return { toast: { error() {} } };
      if (specifier === '@/hooks/useXtermjsSettings') return {
        defaultXtermjsSettings: settings,
        useXtermjsSettings: () => ({ settings, loading: false, error: null }),
      };
      if (specifier === '@/lib/frameThrottle') return {
        frameThrottle: callback => Object.assign(callback, { flush() {}, cancel() {} }),
      };
      if (specifier === './terminalTypes') return {
        createTab: client => ({ id: `tab-${++nextTabId}`, title: client.name, uuid: client.uuid }),
        createTabId: () => `tab-${++nextTabId}`,
      };
      throw new Error(`Unexpected dependency: ${specifier}`);
    },
  }, { filename: sourceFile });
  const useTerminalPage = module.exports.useTerminalPage;

  function flush() {
    let view;
    for (let pass = 0; pass < 20; pass++) {
      changed = false;
      cursor = 0;
      pendingEffects = [];
      view = useTerminalPage();
      const effects = pendingEffects;
      for (const { cell } of effects) cell.cleanup?.();
      for (const kind of ['layout', 'passive']) {
        for (const { cell, effect, deps } of effects.filter(item => item.kind === kind)) {
          cell.deps = deps;
          cell.cleanup = effect();
        }
      }
      if (!changed) return view;
    }
    throw new Error('Hook state did not settle');
  }

  return { flush };
}

function createTerminalApi(name, matchingCount) {
  const listeners = new Set();
  const events = [];
  const emit = resultCount => {
    for (const listener of listeners) listener({ resultIndex: 0, resultCount });
  };
  const searchAddon = {
    clearDecorations() { events.push(`${name}:clear`); },
    onDidChangeResults(listener) {
      events.push(`${name}:subscribe`);
      listeners.add(listener);
      return { dispose() { listeners.delete(listener); events.push(`${name}:unsubscribe`); } };
    },
    findNext(term) {
      events.push(`${name}:find:${term}`);
      emit(matchingCount);
      return matchingCount > 0;
    },
    findPrevious(term) {
      events.push(`${name}:previous:${term}`);
      emit(matchingCount);
      return matchingCount > 0;
    },
  };
  const terminal = {
    options: { theme: {} },
    clearSelection() {}, focus() {},
    onSelectionChange() { return { dispose() {} }; },
  };
  return { api: { searchAddon, terminal, fit() {} }, emit, events, listeners };
}

test('switching terminals receives the synchronous first search result and isolates old results', () => {
  const hook = createHookRunner();
  let page = hook.flush();
  page.openClient({ uuid: 'server-a', name: 'A' });
  page = hook.flush();
  const tabA = page.activeTabId;
  const first = createTerminalApi('A', 2);
  page.handleApiChange(tabA, first.api);
  page = hook.flush();
  page.openSearch();
  page.handleSearchTermChange('ready');
  page = hook.flush();
  assert.equal(page.searchResultCount, 2);

  page.openClient({ uuid: 'server-b', name: 'B' });
  page = hook.flush();
  assert.equal(page.searchResultCount, 0, 'A results must not appear while B has no API');
  const tabB = page.activeTabId;
  const second = createTerminalApi('B', 5);
  page.handleApiChange(tabB, second.api);
  page = hook.flush();
  assert.equal(page.searchResultCount, 5, 'B emitted results synchronously in findNext');
  assert.ok(second.events.indexOf('B:subscribe') < second.events.indexOf('B:find:ready'));
  assert.equal(first.listeners.size, 0, 'A listener is disposed on the terminal switch');

  first.emit(99);
  page = hook.flush();
  assert.equal(page.searchResultCount, 5, 'late A events cannot overwrite B results');

  page.setActiveTabId(tabA);
  page = hook.flush();
  assert.equal(page.searchResultCount, 2, 'returning to A re-runs its current query');
  second.emit(77);
  page = hook.flush();
  assert.equal(page.searchResultCount, 2, 'late B events cannot overwrite A results');
});
