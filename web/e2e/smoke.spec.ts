import { expect, test } from '@playwright/test';

const segment = {
  id: '0123456789abcdef0123456789abcdef',
  startMs: 1704124800000 + 3600000,
  durationMs: 60000,
  videoCodec: 'hevc',
  audioCodec: 'pcm_alaw',
  width: 2560,
  height: 1440,
  fps: 10,
  hasThumbnail: true,
};
const personEvent = {
  id: 'abcdef0123456789abcdef0123456789',
  startMs: segment.startMs,
  endMs: segment.startMs + 60000,
  rawRecordCount: 2,
  hasUnknownFlag: true,
};
const motionEvent = {
  id: 'fedcba9876543210fedcba9876543210',
  startMs: segment.startMs + 30000,
  endMs: segment.startMs + 60000,
  rawRecordCount: 1,
  hasUnknownFlag: false,
};
const folder = {
  id: '11111111111111111111111111111111',
  name: 'Family archive',
  mounted: true,
  scanStatus: 'ready',
  firstDate: '2024-01-02',
};

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/auth/me', (route) =>
    route.fulfill({ contentType: 'application/json', body: '{"user":{"id":"u1","username":"admin","role":"admin","active":true}}' }),
  );
  await page.route('**/api/v1/auth/login', (route) =>
    route.fulfill({ contentType: 'application/json', body: '{"user":{"id":"u1","username":"admin","role":"admin","active":true}}' }),
  );
  await page.route('**/api/v1/auth/password', (route) =>
    route.fulfill({ contentType: 'application/json', body: '{"ok":true}' }),
  );
  await page.route('**/api/v1/folders', (route) =>
    route.fulfill({
      status: route.request().method() === 'POST' ? 201 : 200,
      contentType: 'application/json',
      body: route.request().method() === 'POST'
        ? JSON.stringify({ folder: { ...folder, id: '22222222222222222222222222222222', name: 'Second archive' } })
        : JSON.stringify({ folders: [folder] }),
    }),
  );
  await page.route('**/api/v1/users', (route) =>
    route.fulfill({
      status: route.request().method() === 'POST' ? 201 : 200,
      contentType: 'application/json',
      body: route.request().method() === 'POST'
        ? '{"user":{"id":"u2","username":"viewer","role":"user","active":true}}'
        : '{"users":[{"id":"u1","username":"admin","role":"admin","active":true}]}',
    }),
  );
  await page.route('**/api/v1/days?**', (route) =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        days: [
          { date: '2024-01-02', segments: 12, coverageMs: 720000, events: 2 },
          { date: '2024-01-12', segments: 8, coverageMs: 480000, events: 1 },
        ],
      }),
    }),
  );
  await page.route('**/api/v1/timeline?**', (route) =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ segments: [segment], events: [personEvent, motionEvent] }),
    }),
  );
  await page.route('**/api/v1/media/**', (route) =>
    route.request().method() === 'POST'
      ? route.fulfill({
          contentType: 'application/json',
          body: '{"state":"ready"}',
        })
      : route.fulfill({
          status: 404,
          contentType: 'application/json',
          body: '{"error":"fixture_media"}',
        }),
  );
  await page.goto(`/folders/${folder.id}/player?date=2024-01-02`);
});

test('core controls work without hover', { tag: '@smoke' }, async ({ page }) => {
  await expect(page.getByRole('heading', { name: 'Family archive' })).toBeVisible();
  const timeline = page.getByRole('slider', { name: '24 小时时间线' });
  await expect(timeline).toBeVisible();
  await expect(timeline.locator('..')).toHaveClass(/track/);
  await expect(page.getByLabel('时间轴颜色图例')).toContainText('录像画面变动有人移动无录像');
  await expect(page.locator('.motion-event-range')).toHaveCSS('background-color', 'rgb(255, 189, 74)');
  await expect(page.locator('.person-event-range')).toHaveCSS('background-color', 'rgb(67, 209, 123)');
  await timeline.press('ArrowRight');
  await page.getByRole('button', { name: '2x' }).click();
  await expect(page.getByRole('button', { name: '2x' })).toHaveClass(/active/);
  await page.getByRole('button', { name: /有人移动.*01:00/ }).click();
  await expect(page.getByRole('status')).toBeVisible();
  await expect(page.locator('main')).not.toHaveCSS('overflow-x', 'scroll');
});

test('tapping the timeline seeks to the touched position', async ({ page }, testInfo) => {
  const timeline = page.getByRole('slider', { name: '24 小时时间线' });
  const box = await timeline.boundingBox();
  expect(box).not.toBeNull();
  const point = { x: box!.x + box!.width / 24, y: box!.y + box!.height / 2 };
  if (testInfo.project.name === 'desktop-chromium') {
    await timeline.click({ position: { x: box!.width / 24, y: box!.height / 2 } });
  } else {
    await page.touchscreen.tap(point.x, point.y);
  }
  await expect.poll(async () => Number(await timeline.inputValue())).toBe(3600);
});

test('mobile event selection returns the player to view', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name === 'desktop-chromium', 'mobile interaction only');
  const eventButton = page.getByRole('button', { name: /有人移动.*01:00/ });
  await eventButton.scrollIntoViewIfNeeded();
  await eventButton.click();
  await expect.poll(async () => {
    const box = await page.locator('.player').boundingBox();
    return box !== null && box.y >= 0 && box.y < 80;
  }).toBe(true);
  await expect(page.getByRole('slider', { name: '24 小时时间线' })).toHaveValue('3600');
});

test('days without recordings are disabled', async ({ page }) => {
  await page.getByRole('button', { name: /日期 2024\/01\/02/ }).click();
  const unavailable = page.getByRole('gridcell', { name: '2024年1月11日，无录像' });
  const available = page.getByRole('gridcell', { name: '2024年1月12日，有录像' });
  await expect(unavailable).toBeDisabled();
  await expect(available).toBeEnabled();
  await available.click();
  await expect(page.getByRole('button', { name: /日期 2024\/01\/12/ })).toBeVisible();
  await expect(page).toHaveURL(/date=2024-01-12/);
});

test('media status clears when playback is ready', async ({ page }) => {
  const video = page.locator('video');
  const status = page.getByRole('status');
  await video.dispatchEvent('loadstart');
  await expect(status).toHaveText('正在加载录像…');
  await video.dispatchEvent('waiting');
  await expect(status).toHaveText('正在缓冲…');
  await video.dispatchEvent('canplay');
  await expect(status).toBeEmpty();
  await video.dispatchEvent('waiting');
  await video.dispatchEvent('playing');
  await expect(status).toBeEmpty();
});

test('tries source playback when HEVC capability detection is inconclusive', async ({ page }) => {
  const mediaRequests: string[] = [];
  page.on('request', (request) => {
    if (/\/media\/[^/]+\/(source|compat)$/.test(request.url())) {
      mediaRequests.push(`${request.method()} ${request.url()}`);
    }
  });
  await page.addInitScript(() => {
    const original = HTMLMediaElement.prototype.canPlayType;
    HTMLMediaElement.prototype.canPlayType = function canPlayType(type) {
      if (type.includes('hvc1')) return '';
      return original.call(this, type);
    };
  });
  await page.reload();
  await page.getByRole('button', { name: /有人移动.*01:00/ }).click();
  await expect.poll(() => mediaRequests.some((request) => request.endsWith('/source'))).toBe(true);
  const sourceIndex = mediaRequests.findIndex((request) => request.endsWith('/source'));
  const compatIndex = mediaRequests.findIndex((request) => request.includes('POST') && request.endsWith('/compat'));
  expect(compatIndex === -1 || sourceIndex < compatIndex).toBe(true);
});

test('login form enters folder management', async ({ page }) => {
  await page.goto('/login');
  const username = page.getByLabel('用户名');
  await expect(username).toHaveValue('');
  await username.fill('admin');
  await page.getByLabel('密码').fill('test-password-123!');
  await page.getByRole('button', { name: '登录' }).click();
  await expect(page).toHaveURL(/\/folders$/);
  await expect(page.getByRole('heading', { name: '米家录像回放系统', level: 1 })).toBeVisible();
});

test('login card is centered and readable', async ({ page }) => {
  await page.goto('/login');
  const card = page.locator('.auth-card');
  const box = await card.boundingBox();
  const viewport = page.viewportSize();
  expect(box).not.toBeNull();
  expect(viewport).not.toBeNull();
  expect(box!.width).toBeGreaterThan(300);
  expect(Math.abs(box!.x + box!.width / 2 - viewport!.width / 2)).toBeLessThan(2);
});

test('folder management is usable without hover', async ({ page }) => {
  await page.goto('/folders');
  await expect(page.getByRole('link', { name: /Family archive.*可播放/ })).toBeVisible();
  await page.getByRole('button', { name: '新增文件夹' }).click();
  await page.getByLabel('名称').fill('Second archive');
  await page.getByLabel('服务器路径').fill('/srv/media/second');
  await page.getByRole('button', { name: '保存' }).click();
  await expect(page.locator('main')).not.toHaveCSS('overflow-x', 'scroll');
});

test('administrator can create a user', async ({ page }) => {
  await page.goto('/admin/users');
  await page.getByLabel('用户名').fill('viewer');
  await page.getByLabel('密码', { exact: true }).fill('viewer-password-123!');
  await page.getByLabel('确认密码', { exact: true }).fill('viewer-password-123!');
  await page.getByRole('button', { name: '新增用户' }).click();
  await expect(page.getByRole('status')).toHaveText('用户已创建。');
});

test('new user passwords must match', async ({ page }) => {
  await page.goto('/admin/users');
  await page.getByLabel('用户名').fill('viewer');
  await page.getByLabel('密码', { exact: true }).fill('viewer-password-123!');
  await page.getByLabel('确认密码', { exact: true }).fill('different-password-123!');
  await page.getByRole('button', { name: '新增用户' }).click();
  await expect(page.getByRole('status')).toHaveText('两次输入的密码不一致。');
});

test('signed-in user can change password with the old password', async ({ page }) => {
  let requestBody: Record<string, string> = {};
  await page.route('**/api/v1/auth/password', async (route) => {
    requestBody = route.request().postDataJSON();
    await route.fulfill({ contentType: 'application/json', body: '{"ok":true}' });
  });
  await page.goto('/account/password');
  await page.getByLabel('旧密码').fill('old-password-123!');
  await page.getByLabel('新密码', { exact: true }).fill('new-password-123!');
  await page.getByLabel('确认新密码').fill('new-password-123!');
  await page.getByRole('button', { name: '修改密码' }).click();
  await expect(page).toHaveURL(/\/login\?passwordChanged=1$/);
  await expect(page.getByRole('status')).toHaveText('密码已修改，请使用新密码登录。');
  expect(requestBody).toEqual({
    currentPassword: 'old-password-123!',
    newPassword: 'new-password-123!',
    newPasswordConfirmation: 'new-password-123!',
  });
});

test('wrong old password stays on the password form', async ({ page }) => {
  await page.route('**/api/v1/auth/password', (route) =>
    route.fulfill({ status: 401, contentType: 'application/json', body: '{"error":"invalid_current_password"}' }),
  );
  await page.goto('/account/password');
  await page.getByLabel('旧密码').fill('wrong-password');
  await page.getByLabel('新密码', { exact: true }).fill('new-password-123!');
  await page.getByLabel('确认新密码').fill('new-password-123!');
  await page.getByRole('button', { name: '修改密码' }).click();
  await expect(page).toHaveURL(/\/account\/password$/);
  await expect(page.getByRole('alert')).toHaveText('旧密码错误。');
});
