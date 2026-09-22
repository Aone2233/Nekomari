import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import vm from 'node:vm';
import test from 'node:test';
import React from 'react';
import { renderToString } from 'react-dom/server';
import ts from 'typescript';

const require = createRequire(import.meta.url);
const root = fileURLToPath(new URL('../src/contexts/', import.meta.url));
const modules = new Map();
function load(file) {
  file = path.resolve(file);
  if (modules.has(file)) return modules.get(file).exports;
  const module = { exports: {} };
  modules.set(file, module);
  const code = ts.transpileModule(readFileSync(file, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true },
  }).outputText;
  vm.runInNewContext(code, {
    module, exports: module.exports, console,
    require(spec) {
      if (spec.endsWith('/i18n/config')) return { default: { t: key => key }, __esModule: true };
      if (spec.endsWith('/lib/rpc2')) return { RPC2Client: class { state = 'disconnected'; } };
      if (!spec.startsWith('.')) return require(spec);
      const target = path.resolve(path.dirname(file), spec);
      if (target.endsWith('.json')) return JSON.parse(readFileSync(target, 'utf8'));
      const resolved = ['.ts', '.tsx'].map(ext => target + ext).find(existsSync);
      assert.ok(resolved, spec);
      return load(resolved);
    },
  }, { filename: file });
  return module.exports;
}

// Exercise real React context identity across the new module boundaries. Effects
// intentionally do not run in SSR; HTTP/connection lifecycles are separate tests.
for (const [name, provider, hook, field] of [
  ['Account', 'AccountProvider', 'useAccount', 'account'],
  ['AdminNavigation', 'AdminNavigationProvider', 'useAdminNavigation', 'refreshVersion'],
  ['CommandClipboard', 'CommandClipboardProvider', 'useCommandClipboard', 'commands'],
  ['LiveData', 'LiveDataProvider', 'useLiveData', 'live_data'],
  ['LoadAlert', 'LoadAlertProvider', 'useLoadAlert', 'loadAlerts'],
  ['NodeDetails', 'NodeDetailsProvider', 'useNodeDetails', 'nodeDetail'],
  ['NodeList', 'NodeListProvider', 'useNodeList', 'nodeList'],
  ['Notification', 'OfflineNotificationProvider', 'useOfflineNotification', 'offlineNotification'],
  ['PingTask', 'PingTaskProvider', 'usePingTask', 'pingTasks'],
  ['PublicInfo', 'PublicInfoProvider', 'usePublicInfo', 'publicInfo'],
  ['RPC2', 'RPC2Provider', 'useRPC2', 'client'],
]) {
  test(name + ' consumer resolves its split provider', () => {
    const Provider = load(path.join(root, name + 'Provider.tsx'))[provider];
    const useValue = load(path.join(root, name + 'Context.ts'))[hook];
    let value;
    const Consumer = () => { value = useValue(); return React.createElement('span', null, 'connected'); };
    let tree = React.createElement(Provider, null, React.createElement(Consumer));
    if (name === 'LiveData' || name === 'NodeList') {
      const RPC2Provider = load(path.join(root, 'RPC2Provider.tsx')).RPC2Provider;
      tree = React.createElement(RPC2Provider, null, tree);
    }
    assert.match(renderToString(tree), /connected/);
    assert.ok(Object.hasOwn(value, field));
    if (name === 'LiveData') assert.equal(value.showCallout, false); // default context is true
  });
}
