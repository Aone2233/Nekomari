import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';
import test from 'node:test';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import ts from 'typescript';

const require = createRequire(import.meta.url);
let now = Date.UTC(2026, 8, 23, 12);
const day = 24 * 60 * 60 * 1000;
class FixedDate extends Date {
  static now() { return now; }
}

function loadComponent(name, environment = {}) {
  const file = fileURLToPath(new URL(`../src/components/${name}.tsx`, import.meta.url));
  const code = ts.transpileModule(readFileSync(file, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true },
  }).outputText;
  const module = { exports: {} };
  vm.runInNewContext(code, {
    module, exports: module.exports, Date: FixedDate, window: environment.window,
    require(spec) {
      if (spec === 'react' || spec === 'react/jsx-runtime') return require(spec);
      if (spec === '@radix-ui/themes') return {
        Flex: ({ children }) => React.createElement('div', null, children),
        Badge: ({ color, children }) => React.createElement('span', { 'data-color': color }, children),
      };
      if (spec === 'react-i18next') return {
        useTranslation: () => [(key, options) => options?.days === undefined ? key : `${key}:${options.days}`],
      };
      if (spec === '@/contexts/AccountProvider') return { AccountProvider: ({ children }) => children };
      if (spec === '@/contexts/AccountContext') return {
        useAccount: () => ({ account: { logged_in: false }, loading: false }),
      };
      if (spec === '@/contexts/PublicInfoContext') return {
        usePublicInfo: () => ({ publicInfo: { oauth_enable: true, disable_password_login: true } }),
      };
      return {};
    },
  }, { filename: file });
  return module.exports.default;
}

test('login keeps its inner component identity across parent renders and resets on autoOpen transitions', () => {
  const LoginDialog = loadComponent('Login');
  const manual = LoginDialog({ autoOpen: false }).props.children;
  const rerender = LoginDialog({ autoOpen: false, info: 'changed' }).props.children;
  const auto = LoginDialog({ autoOpen: true }).props.children;
  assert.equal(manual.type, rerender.type);
  assert.equal(manual.type, auto.type);
  assert.equal(manual.key, rerender.key);
  assert.notEqual(manual.key, auto.key);
});

test('price labels and urgency color use one clock snapshot at expiry boundaries', () => {
  const PriceTags = loadComponent('PriceTags');
  const render = days => renderToStaticMarkup(
    React.createElement(PriceTags, { price: 10, expired_at: now + days * day }),
  );
  assert.match(render(7), /data-color="red"[^>]*>.*common.expired_in:7/);
  assert.match(render(8), /data-color="orange"[^>]*>.*common.expired_in:8/);
  assert.match(render(16), /data-color="green"[^>]*>.*common.expired_in:16/);
  assert.match(render(0), /common.expired/);
  assert.match(renderToStaticMarkup(React.createElement(PriceTags, { price: 10 })), /common.expired_in:30/);
});

test('custom OAuth trigger responds to Enter and Space as well as pointer activation', () => {
  const location = { href: '/index' };
  const LoginDialog = loadComponent('Login', { window: { location } });
  const InnerLayout = LoginDialog({}).props.children.type;
  let trigger;
  renderToStaticMarkup(React.createElement(() => {
    trigger = InnerLayout({ trigger: React.createElement('span', null, 'Custom login') });
    return trigger;
  }));
  assert.equal(trigger.type, 'span');
  assert.equal(trigger.props.role, 'button');
  assert.equal(trigger.props.tabIndex, 0);
  for (const key of ['Enter', ' ']) {
    let prevented = false;
    trigger.props.onKeyDown({ key, preventDefault() { prevented = true; } });
    assert.equal(prevented, true);
    assert.equal(location.href, '/api/oauth');
    location.href = '/index';
  }
  trigger.props.onKeyDown({ key: 'Escape', preventDefault() { assert.fail('unexpected navigation'); } });
  assert.equal(location.href, '/index');
  trigger.props.onClick();
  assert.equal(location.href, '/api/oauth');
});

test('native OAuth button remains a single control and preserves its click handler', () => {
  const location = { href: '/index' };
  const LoginDialog = loadComponent('Login', { window: { location } });
  const InnerLayout = LoginDialog({}).props.children.type;
  let originalClicks = 0;
  let trigger;
  renderToStaticMarkup(React.createElement(() => {
    trigger = InnerLayout({ trigger: React.createElement('button', {
      type: 'button', onClick: () => { originalClicks += 1; },
    }, 'Custom login') });
    return trigger;
  }));
  assert.equal(trigger.type, 'button');
  assert.equal(trigger.props.type, 'button');
  let prevented = false;
  trigger.props.onClick({ defaultPrevented: false, preventDefault() { prevented = true; } });
  assert.equal(originalClicks, 1);
  assert.equal(prevented, true);
  assert.equal(location.href, '/api/oauth');
});
