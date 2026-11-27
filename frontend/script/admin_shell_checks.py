from pathlib import PurePosixPath
from urllib.parse import urlsplit

from playwright.sync_api import expect


def check_admin_shell(browser, session, origin):
    context = browser.new_context(base_url=origin, storage_state=session.storage_state(),
                                  locale='zh-CN', service_workers='block')
    try:
        context.add_init_script("localStorage.setItem('language','zh-CN')")
        release_url = 'https://github.com/Aone2233/Nekomari/releases/tag/v999.0.0'
        context.route('https://api.github.com/repos/Aone2233/Nekomari/releases?*', lambda route:
                      route.fulfill(json=[dict(tag_name='v999.0.0', name='Fixture release',
                                               body='Fixture release notes', html_url=release_url)]))
        page = context.new_page()
        chunks = []
        page.on('request', lambda req: chunks.append(PurePosixPath(urlsplit(req.url).path).name)
                if req.resource_type == 'script' else None)

        def count(prefix):
            return sum(name.startswith(prefix + '-') for name in chunks)

        page.goto('/admin/servers')
        expect(page.get_by_test_id('node-row').first).to_be_visible()
        trigger = page.get_by_role('button', name='有新版本', exact=True)
        expect(trigger).to_be_visible()
        assert not any(count(prefix) for prefix in ('eula', 'EulaContent', 'ReleaseNotesContent'))
        trigger.focus()
        trigger.press('Enter')
        notes = page.get_by_role('dialog', name='有新版本')
        expect(notes.get_by_text('Fixture release notes')).to_be_visible()
        expect(notes.get_by_role('link', name='GitHub')).to_have_attribute('href', release_url)
        assert count('ReleaseNotesContent') == 1
        page.keyboard.press('Escape')
        expect(trigger).to_be_focused()

        assert context.request.post('/api/admin/settings/', data={'eula_accepted': False}).json()['status'] == 'success'
        page.reload()
        notice = page.get_by_role('dialog', name='法律声明与合规指引')
        expect(notice.locator('pre')).to_contain_text('重要提示')
        assert count('EulaContent') == 1
        accept = notice.get_by_role('button', name='我已详细阅读并接受')
        page.route('**/api/admin/settings', lambda route: route.fulfill(status=503, body='unavailable')
                   if route.request.method == 'POST' else route.continue_())
        with page.expect_response(lambda res: res.request.method == 'POST' and res.status == 503):
            accept.click()
        expect(accept).to_be_visible()
        page.unroute('**/api/admin/settings')
        accept.click()
        expect(notice).to_be_hidden()
        assert context.request.get('/api/admin/settings').json()['data']['eula_accepted'] is True
        print('PASS: accepted EULA and unopened update notes absent on list; keyboard notes, EULA failure and retry')
    finally:
        context.close()
