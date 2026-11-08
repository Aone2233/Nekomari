import os
from playwright.sync_api import sync_playwright

base = os.environ.get('THEME_FIXTURE_URL', 'http://127.0.0.1:5236/ping-role.browser.html')
with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, args=['--no-sandbox'])
    for width, enabled in ((1440, False), (390, False), (1440, True), (390, True)):
        page = browser.new_page(viewport={'width': width, 'height': 900})
        errors = []
        page.on('pageerror', lambda error: errors.append(str(error)))
        page.goto(base + ('?reference=1' if enabled else ''), wait_until='networkidle')
        page.get_by_text('Education IPv6 (IPV6 / ICMP / 主目标)', exact=True).wait_for()
        page.get_by_text('Education IPv6 (IPV6 / TCP / 主目标)', exact=True).wait_for()
        reference = page.get_by_text('Education IPv6 (IPV6 / ICMP / 参考点)', exact=True)
        assert reference.count() == (1 if enabled else 0)
        assert '角色未标记' not in page.locator('body').inner_text()
        def lines():
            return [s for s in page.evaluate('window.chartSeries()') if s['type'] == 'line']
        series = lines()
        assert len(series) == (4 if enabled else 3)
        expected = {'主目标)': {192, 195}, 'IPv6 TCP': {164}}
        if enabled:
            expected['参考点)'] = {2}
        for label, values in expected.items():
            selected = [s for s in series if label in s['name']]
            assert {s['data'][0][1] for s in selected} == values, selected
        if enabled:
            reference.click()
            assert len(lines()) == 3
        for period in ('7 天', '30 天'):
            page.get_by_role('tab', name=period, exact=True).click()
            assert len(lines()) == (4 if enabled else 3)
            page.get_by_role('button', name='平滑峰值').click()
            assert all(s['data'] for s in lines())
            page.get_by_role('button', name='平滑峰值').click()
            assert all(s['data'] for s in lines())
        assert not errors and not page.evaluate('window.fixtureErrors'), errors
        print(f'viewport {width}, reference={enabled}: roles, selection, 7/30-day smoothing passed')
        page.close()
    browser.close()
