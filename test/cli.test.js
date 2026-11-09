import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { App, APIError, normalizeOrigin, configPath } from '../src/cli.js';
import { parseJSON, stringifyJSON } from '../src/json.js';

const root = fileURLToPath(new URL('../', import.meta.url));
const capture = () => ({ value: '', write(chunk) { this.value += chunk; } });
function cred(base, overrides = {}) {
  return { baseUrl: base, accessToken: 'synthetic-access', refreshToken: 'synthetic-refresh', expiresAt: new Date(Date.now() + 3600000).toISOString(), tenantId: 7, scope: 'flowboom.read flowboom.drafts flowboom.publish', ...overrides };
}
async function fixture(t, handler, options = {}) {
  const dir = await fs.mkdtemp(path.join(tmpdir(), 'flowboom-node-'));
  const requests = [];
  const server = http.createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const body = Buffer.concat(chunks).toString();
    requests.push({ path: req.url, method: req.method, tenant: req.headers['x-tenant-id'], token: req.headers.authorization, body });
    res.setHeader('Content-Type', 'application/json');
    try { await handler(req, res, body, requests); } catch (error) { t.diagnostic(error.message); res.statusCode = 500; res.end('{}'); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); await fs.rm(dir, { recursive: true, force: true }); });
  const out = capture(), err = capture();
  const app = new App({ configDir: path.join(dir, 'private'), out, err, env: {}, ...options });
  await app.save(cred(base));
  return { app, base, requests, out, err, dir };
}

test('Node entrypoint exposes flowboom help/version without credentials', () => {
  const help = spawnSync(process.execPath, [path.join(root, 'bin/flowboom.js'), 'help'], { encoding: 'utf8' });
  assert.equal(help.status, 0); assert.match(help.stdout, /flowboom \[--profile/); assert.match(help.stdout, /articles schedule/);
  const version = spawnSync(process.execPath, [path.join(root, 'bin/flowboom.js'), '--version'], { encoding: 'utf8' });
  assert.equal(version.status, 0); assert.equal(version.stdout.trim(), '0.1.0');
});

test('Go credential OS paths remain compatible', () => {
  assert.equal(configPath('cn', { XDG_CONFIG_HOME: '/tmp/config' }, 'linux', '/home/demo'), '/tmp/config/flowboom/cn.json');
  assert.equal(configPath('global', {}, 'darwin', '/Users/demo'), path.posix.join('/Users/demo', 'Library', 'Application Support', 'flowboom', 'global.json'));
  assert.throws(() => configPath('cn', { XDG_CONFIG_HOME: 'relative' }, 'linux', '/home/demo'));
});

test('origin validation and redirect refusal never forward credentials', async t => {
  for (const value of ['http://example.test', 'https://u:p@example.test', 'https://example.test/path', 'https://example.test/?x=1', 'file:///tmp/x']) assert.throws(() => normalizeOrigin(value));
  assert.equal(normalizeOrigin('https://cloud.flowboom.cn/'), 'https://cloud.flowboom.cn');
  assert.equal(normalizeOrigin('http://[::1]:1234'), 'http://[::1]:1234');
  let otherRequests = 0;
  const other = await fixture(t, (_req, res) => { otherRequests++; res.end('{}'); });
  const f = await fixture(t, (_req, res) => { res.statusCode = 307; res.setHeader('Location', other.base + '/stolen'); res.end('{}'); });
  await assert.rejects(f.app.run(['sites', 'list']), /HTTP 307/);
  assert.equal(otherRequests, 0); assert.equal(f.requests.length, 1);
});

test('profiles isolate credentials, origins and workspace overrides', async t => {
  const f = await fixture(t, (_req, res) => res.end('{"data":[]}'));
  const other = await fixture(t, (_req, res) => res.end('{"data":[]}'));
  f.app.profile = 'global'; await f.app.save(cred(other.base, { accessToken: 'global-access', tenantId: 9 }));
  await f.app.run(['--profile', 'cn', 'sites', 'list']);
  await f.app.run(['--profile', 'global', 'sites', 'list']);
  assert.equal(f.requests[0].token, 'Bearer synthetic-access'); assert.equal(other.requests[0].token, 'Bearer global-access');
  assert.equal(other.requests[0].tenant, '9'); assert.doesNotMatch(f.out.value + f.err.value, /synthetic-access|synthetic-refresh|global-access/);
});

test('legacy Go JSON loads with private credentials and exact uint64 IDs', async t => {
  const id = '18446744073709551615';
  assert.equal(stringifyJSON(parseJSON(`{"id":${id}}`)), `{"id":${id}}`);
  assert.throws(() => parseJSON('{"id":1e20}'), /Unsafe integer/);
  const f = await fixture(t, (_req, res) => res.end(`{"data":{"id":${id}}}`));
  await fs.writeFile(f.app.credentialPath, stringifyJSON(cred(f.base, { tenantId: BigInt(id) })), { mode: 0o600 });
  await f.app.run(['articles', 'get', id]);
  assert.equal(f.requests[0].tenant, id); assert.match(f.out.value, new RegExp(id));
  assert.equal((await f.app.load()).tenantIds[0], BigInt(id));
  if (process.platform !== 'win32') {
    assert.equal((await fs.stat(f.app.credentialPath)).mode & 0o777, 0o600);
    assert.equal((await fs.stat(path.dirname(f.app.credentialPath))).mode & 0o777, 0o700);
    await fs.chmod(f.app.credentialPath, 0o644);
    await assert.rejects(f.app.load(), /private file/);
  }
});

test('refresh rotates on pinned origin and retains selected workspace', async t => {
  const f = await fixture(t, (req, res) => {
    if (req.url === '/api/v1/oauth/token') return res.end('{"access_token":"next-access","refresh_token":"next-refresh","expires_in":3600,"tenant_id":7,"tenant_ids":[7,8],"scope":"flowboom.read"}');
    res.end('{"data":[]}');
  });
  await f.app.save(cred(f.base, { expiresAt: '2020-01-01T00:00:00Z', tenantId: 8, tenantIds: [7, 8] }));
  await f.app.run(['sites', 'list']);
  assert.equal(f.requests.length, 2); assert.equal(f.requests[1].token, 'Bearer next-access'); assert.equal(f.requests[1].tenant, '8');
  const saved = await f.app.load(); assert.equal(saved.refreshToken, 'next-refresh'); assert.equal(saved.baseUrl, f.base);
  await f.app.run(['--workspace', '7', 'sites', 'list']);
  assert.equal((await f.app.load()).tenantId, 8); assert.equal(f.requests.at(-1).tenant, '7');
  const before = f.requests.length;
  await assert.rejects(f.app.run(['sites', 'list', '--workspace', '9']), /not authorized/);
  assert.equal(f.requests.length, before);
});

test('workspace listing is independent of default membership and use verifies target', async t => {
  const f = await fixture(t, (_req, res) => res.end('{"data":[]}'));
  await f.app.save(cred(f.base, { tenantIds: [7, 8] }));
  await f.app.run(['workspaces', 'list']); assert.equal(f.requests[0].tenant, undefined);
  await f.app.run(['workspaces', 'use', '8']); assert.equal(f.requests[1].tenant, '8'); assert.equal((await f.app.load()).tenantId, 8);
  await assert.rejects(f.app.run(['workspaces', 'use', '9']), /not authorized/);
});

test('refresh never expands consent snapshot', async t => {
  const f = await fixture(t, (_req, res) => res.end('{"access_token":"next","refresh_token":"next-refresh","expires_in":3600,"tenant_id":7,"tenant_ids":[7,9],"scope":"flowboom.read"}'));
  await f.app.save(cred(f.base, { expiresAt: '2020-01-01T00:00:00Z' }));
  await assert.rejects(f.app.run(['sites', 'list']), /expanded workspaces/);
  assert.equal(f.requests.length, 1);
});

test('device login handles pending/slow_down, approval and denial without leaking tokens', async t => {
  for (const denied of [false, true]) {
    let polls = 0, now = Date.now(); const waits = [];
    const f = await fixture(t, (req, res) => {
      if (req.url === '/api/v1/oauth/device/code') return res.end(stringifyJSON({ device_code: 'synthetic-device', user_code: 'ABCD-EFGH', verification_uri_complete: f.base + '/oauth/device?user_code=ABCD-EFGH', expires_in: 600, interval: 5 }));
      if (req.url === '/api/v1/oauth/token') {
        polls++;
        if (denied) { res.statusCode = 400; return res.end('{"error":"access_denied"}'); }
        if (polls < 3) { res.statusCode = 400; return res.end(stringifyJSON({ error: polls === 1 ? 'authorization_pending' : 'slow_down' })); }
        return res.end('{"access_token":"login-access","refresh_token":"login-refresh","expires_in":3600,"tenant_id":7,"scope":"flowboom.read"}');
      }
      res.end('{"data":{"tenant":{"id":7}}}');
    }, { now: () => now, sleepImpl: async ms => { waits.push(ms); now += ms; } });
    await fs.unlink(f.app.credentialPath);
    if (denied) {
      await assert.rejects(f.app.run(['auth', 'login', '--no-browser', '--base-url', f.base]), /denied/);
      await assert.rejects(fs.stat(f.app.credentialPath), { code: 'ENOENT' });
    } else {
      await f.app.run(['auth', 'login', '--no-browser', '--base-url', f.base]);
      assert.equal((await f.app.load()).accessToken, 'login-access'); assert.deepEqual(waits, [5000, 5000, 10000]);
      assert.equal(new URLSearchParams(f.requests[0].body).get('client_id'), 'flowboom-cli');
    }
    assert.doesNotMatch(f.out.value + f.err.value, /login-access|login-refresh/);
  }
});

test('device verification URL must match selected origin before browser opens', async t => {
  let opened = false;
  const f = await fixture(t, (_req, res) => res.end('{"device_code":"x","user_code":"ABCD-EFGH","verification_uri_complete":"https://evil.test/oauth/device","expires_in":600,"interval":5}'), { openBrowser: () => { opened = true; } });
  await assert.rejects(f.app.run(['auth', 'login', '--base-url', f.base]), /does not match/);
  assert.equal(opened, false); assert.equal(f.requests.length, 1);
});

test('publish defaults to preview, rejects unreviewed articles and never retries writes', async t => {
  for (const state of ['draft', 'ready']) {
    const f = await fixture(t, (req, res) => {
      if (req.method === 'GET') return res.end(stringifyJSON({ data: { id: 12, siteId: 9, title: 'Exact target', status: state } }));
      res.statusCode = 503; res.end('{"error":"SECRET-PROVIDER-ERROR"}');
    });
    await f.app.run(['articles', 'publish', '12']); assert.equal(f.requests.length, 1); assert.match(f.out.value, /confirmationRequired/);
    await assert.rejects(f.app.run(['articles', 'publish', '12', '--confirm']), error => !error.message.includes('SECRET'));
    assert.equal(f.requests.filter(r => r.method === 'POST').length, state === 'ready' ? 1 : 0);
  }
});

test('draft and schedule files retain large IDs; only --confirm authorizes plan writes', async t => {
  const f = await fixture(t, (_req, res) => res.end('{"data":{"confirmed":false}}'));
  const file = path.join(f.dir, 'plan.json');
  await fs.writeFile(file, '{"siteId":9,"articleIds":[18446744073709551615],"confirm":true}');
  for (const action of ['schedule', 'unschedule']) {
    await f.app.run(['articles', action, '--file', file]);
    assert.match(f.requests.at(-1).body, /18446744073709551615/); assert.equal(parseJSON(f.requests.at(-1).body).confirm, false);
    await f.app.run(['articles', action, '--file', file, '--confirm']); assert.equal(parseJSON(f.requests.at(-1).body).confirm, true);
  }
  await fs.writeFile(file, '{"siteId":9,"title":"Test","slug":"test","contentMarkdown":"# Body"}');
  await f.app.run(['articles', 'create', '--file', file]); assert.equal(f.requests.at(-1).path, '/api/v1/skills/articles');
});

test('all operational command families retain their API routes', async t => {
  const f = await fixture(t, (_req, res) => res.end('{"data":[]}'));
  const cases = [
    [['articles', 'submit-review', '12'], 'POST', '/articles/12/submit-review'],
    [['publish-tasks', 'get', '3'], 'GET', '/publish-tasks/3'],
    [['categories', 'list', '--site', '9'], 'GET', '/categories?siteId=9'],
    [['sources', 'list', '--site', '9', '--status', 'new'], 'GET', '/source-items?siteId=9&page=1&status=new'],
    [['articles', 'list', '--query', 'exact title'], 'GET', '/articles?page=1&pageSize=20&q=exact+title'],
    [['topics', 'list'], 'GET', '/topics?page=1&pageSize=20'],
    [['briefs', 'get', '12'], 'GET', '/briefs/12'],
    [['knowledge', 'search', '--query', 'term'], 'POST', '/knowledge/search'],
    [['diagnostics'], 'GET', '/diagnostics'],
    [['articles', 'plan', '--site', '9'], 'GET', '/publication-plan?siteId=9&page=1&pageSize=20'],
    [['seo', 'report', '--site', '9', '--engine', 'bing'], 'GET', '/sites/9/seo/overview?engine=bing&source=ga4'],
    [['seo', 'opportunities', '--site', '9'], 'GET', '/sites/9/seo/opportunities'],
  ];
  for (const [args, method, endpoint] of cases) {
    await f.app.run(args); assert.equal(f.requests.at(-1).method, method); assert.equal(f.requests.at(-1).path, '/api/v1/skills' + endpoint);
  }
  for (const args of [['articles', 'approve', '12'], ['sites', 'list', '--confirm'], ['articles', 'publish'], ['sites', 'list', '--unknown'], ['auth', 'logout', 'extra']]) await assert.rejects(f.app.run(args));
});

test('logout retains local credential on failed revocation and clears only selected profile on success', async t => {
  let fail = true;
  const f = await fixture(t, (_req, res) => { res.statusCode = fail ? 503 : 200; res.end('{}'); });
  await assert.rejects(f.app.run(['auth', 'logout']), /retained/);
  assert.equal((await f.app.load()).refreshToken, 'synthetic-refresh');
  f.app.profile = 'global'; await f.app.save(cred(f.base)); f.app.profile = 'cn';
  fail = false; await f.app.run(['auth', 'logout']);
  await assert.rejects(fs.stat(f.app.credentialPath), { code: 'ENOENT' });
  assert.ok(await fs.stat(path.join(f.dir, 'private', 'global.json')));
});

test('request timeout/oversize and error bodies remain bounded and redacted', async t => {
  const f = await fixture(t, (_req, res) => { res.statusCode = 500; res.end('SECRET-BACKEND-TEXT'); });
  await assert.rejects(f.app.run(['sites', 'list']), error => error instanceof APIError && !error.message.includes('SECRET'));
  const large = await fixture(t, (_req, res) => res.end('x'.repeat(8 * 1024 * 1024 + 1)));
  await assert.rejects(large.app.run(['sites', 'list']), /oversized/);
});
