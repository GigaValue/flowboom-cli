import * as fs from 'node:fs/promises';
import { homedir } from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { parseArgs } from 'node:util';
import { setTimeout as sleep } from 'node:timers/promises';
import { parseJSON, stringifyJSON } from './json.js';

export const origins = Object.freeze({ cn: 'https://cloud.flowboom.cn', global: 'https://cloud.flowboom.io' });
const clientID = 'flowboom-cli'; // Keep the deployed OAuth protocol ID; executable/package are flowboom.
const scopes = 'flowboom.read flowboom.knowledge flowboom.drafts flowboom.publish';
const MAX_RESPONSE = 8 * 1024 * 1024;
const MAX_FILE = 1024 * 1024;
const safeErrors = new Map([
  ['configure an enabled publish_sweep for this site before confirming a publication plan', 'This site needs an enabled publish_sweep pipeline; configure it in Flowboom before saving the plan.'],
  ['all selected articles must be reviewed ready SEO articles', 'All selected articles must be reviewed ready SEO articles; finish approval in Flowboom first.'],
  ['only scheduled ready, draft, reviewing or rejected articles can be unscheduled', 'Only scheduled unpublished articles can be unscheduled; inspect article and task status.'],
  ['select 1-100 unique articles, a future RFC3339 startAt with timezone, intervalDays 1-365 and perDay 1-20', 'Invalid plan: select 1-100 unique articles, a future time with timezone, intervalDays 1-365 and perDay 1-20.'],
  ['only reviewed ready articles can be published', 'Article must pass review before publication.'],
  ['SEO score is below the site publish threshold', 'Article did not pass site SEO checks; inspect it in Flowboom.'],
  ['SEO error-level checks must pass before publish', 'Article did not pass site SEO checks; inspect it in Flowboom.'],
]);
export class APIError extends Error {
  constructor(status, code) {
    super(safeErrors.get(code) ?? `Request failed (HTTP ${status}); check authorization, workspace and permissions.`);
    this.status = status;
    this.code = code;
  }
}
export function positiveID(value) {
  const raw = String(value);
  if (!/^\d+$/.test(raw) || BigInt(raw) < 1n || BigInt(raw) > 18446744073709551615n) throw new Error('Positive uint64 ID required.');
  if (typeof value === 'number' && !Number.isSafeInteger(value)) throw new Error('Unsafe numeric ID.');
  return BigInt(raw).toString();
}
function numericID(value) {
  const id = BigInt(positiveID(value));
  return id <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(id) : id;
}
function hasID(ids, value) { return ids.some(id => String(id) === String(value)); }
export function normalizeOrigin(raw) {
  let url;
  try { url = new URL(raw); } catch { throw new Error('Base URL must be an HTTPS origin.'); }
  if (url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('Base URL must be an HTTPS origin.');
  const host = url.hostname.replace(/^\[|\]$/g, '');
  const loopback = host === 'localhost' || host === '::1' || /^127\./.test(host);
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback)) throw new Error('HTTPS is required except for loopback demos.');
  return url.origin;
}
export function configPath(profile, env = process.env, platform = process.platform, home = homedir()) {
  const paths = platform === 'win32' ? path.win32 : path.posix;
  const dir = platform === 'darwin' ? paths.join(home, 'Library', 'Application Support') : platform === 'win32' ? env.APPDATA : env.XDG_CONFIG_HOME || paths.join(home, '.config');
  if (!dir || !paths.isAbsolute(dir)) throw new Error('An absolute OS user configuration directory is required.');
  return paths.join(dir, 'flowboom', `${profile}.json`);
}
function credentials(raw) {
  if (!raw || typeof raw.accessToken !== 'string' || !raw.accessToken || typeof raw.refreshToken !== 'string' || !raw.refreshToken || typeof raw.scope !== 'string' || !Number.isFinite(Date.parse(raw.expiresAt))) throw new Error('Invalid credentials; log in again.');
  const tenantId = numericID(raw.tenantId);
  const tenantIds = raw.tenantIds?.length ? raw.tenantIds.map(numericID) : [tenantId];
  if (!hasID(tenantIds, tenantId)) throw new Error('Invalid default workspace; log in again.');
  return { baseUrl: normalizeOrigin(raw.baseUrl), accessToken: raw.accessToken, refreshToken: raw.refreshToken, expiresAt: raw.expiresAt, tenantId, tenantIds, scope: raw.scope };
}
function browserOpen(url) {
  const [command, args] = process.platform === 'darwin' ? ['open', [url]] : process.platform === 'win32' ? ['rundll32', ['url.dll,FileProtocolHandler', url]] : ['xdg-open', [url]];
  const child = spawn(command, args, { detached: true, stdio: 'ignore', shell: false });
  child.on('error', () => {});
  child.unref();
}
const options = Object.fromEntries(['profile', 'workspace', 'base-url', 'site', 'file', 'query', 'status', 'page', 'page-size', 'from', 'to', 'engine', 'source'].map(name => [name, { type: 'string' }]));
for (const name of ['confirm', 'no-browser', 'read-only', 'help', 'version']) options[name] = { type: 'boolean' };
options.help.short = 'h';
options.version.short = 'v';

export class App {
  constructor({ env = process.env, configDir, out = process.stdout, err = process.stderr, fetchImpl = globalThis.fetch, signal, sleepImpl = sleep, openBrowser = browserOpen, now = Date.now } = {}) {
    Object.assign(this, { env, configDir, out, err, fetch: fetchImpl, signal, sleep: sleepImpl, openBrowser, now });
    this.profile = env.FLOWBOOM_PROFILE || 'cn';
    this.workspace = undefined;
  }
  get credentialPath() { return this.configDir ? path.join(this.configDir, `${this.profile}.json`) : configPath(this.profile, this.env); }
  async load() {
    let info;
    try { info = await fs.lstat(this.credentialPath); } catch { throw new Error('Not logged in; run flowboom auth login for this profile.'); }
    if (!info.isFile() || (process.platform !== 'win32' && (info.mode & 0o077))) throw new Error('Credential file must be a regular private file (0600).');
    try { return credentials(parseJSON(await fs.readFile(this.credentialPath, 'utf8'))); } catch { throw new Error('Invalid credentials; log in again.'); }
  }
  async save(value) {
    const dir = path.dirname(this.credentialPath);
    let temporary;
    try {
      await fs.mkdir(dir, { recursive: true, mode: 0o700 });
      const info = await fs.lstat(dir);
      if (!info.isDirectory() || (process.platform !== 'win32' && (info.mode & 0o077))) throw new Error('private directory');
      temporary = path.join(dir, `.credentials-${randomUUID()}`);
      await fs.writeFile(temporary, stringifyJSON(value), { mode: 0o600, flag: 'wx' });
      await fs.rename(temporary, this.credentialPath);
    } catch { throw new Error('Cannot save credentials; configuration directory must be private (0700).'); }
    finally { if (temporary) await fs.rm(temporary, { force: true }).catch(() => {}); }
  }
  selected(c) {
    const id = this.workspace ?? c.tenantId;
    if (!hasID(c.tenantIds, id)) throw new Error('Workspace not authorized; choose an authorized workspace or log in again.');
    return id;
  }
  async request(base, method, endpoint, { body, contentType, token, tenant } = {}) {
    const headers = { Accept: 'application/json', 'User-Agent': 'flowboom/0.1.0' };
    if (contentType) headers['Content-Type'] = contentType;
    if (token) headers.Authorization = `Bearer ${token}`;
    if (tenant !== undefined) headers['X-Tenant-ID'] = positiveID(tenant);
    let response;
    try {
      const signal = this.signal ? AbortSignal.any([this.signal, AbortSignal.timeout(30000)]) : AbortSignal.timeout(30000);
      response = await this.fetch(`${base}/api/v1${endpoint}`, { method, headers, body, redirect: 'manual', signal });
    } catch { throw new Error('API connection failed. Writes are not retried: inspect article, task or plan state before resubmitting.'); }
    const chunks = []; let size = 0;
    try {
      for await (const chunk of response.body ?? []) {
        size += chunk.length;
        if (size > MAX_RESPONSE) throw new Error('oversized');
        chunks.push(chunk);
      }
    } catch { throw new Error('Invalid or oversized API response.'); }
    let data;
    try { data = parseJSON(Buffer.concat(chunks).toString('utf8')); } catch {
      if (!response.ok) throw new APIError(response.status);
      throw new Error('API did not return JSON; check deployment.');
    }
    if (!response.ok) throw new APIError(response.status, data?.error);
    return data;
  }
  form(base, endpoint, values) {
    return this.request(base, 'POST', endpoint, { body: new URLSearchParams({ ...values, client_id: clientID }).toString(), contentType: 'application/x-www-form-urlencoded' });
  }
  async exchange(base, values) {
    const t = await this.form(base, '/oauth/token', values);
    if (!Number.isSafeInteger(t?.expires_in) || t.expires_in <= 0 || t.expires_in > 3600) throw new Error('Invalid token response.');
    try { return credentials({ baseUrl: base, accessToken: t.access_token, refreshToken: t.refresh_token, expiresAt: new Date(this.now() + t.expires_in * 1000).toISOString(), tenantId: t.tenant_id, tenantIds: t.tenant_ids, scope: t.scope }); }
    catch { throw new Error('Invalid token response.'); }
  }
  async ready() {
    const c = await this.load();
    if (Date.parse(c.expiresAt) - this.now() > 60000) return c;
    let next;
    try { next = await this.exchange(c.baseUrl, { grant_type: 'refresh_token', refresh_token: c.refreshToken }); }
    catch { throw new Error('Session refresh failed; retry once or run flowboom auth login.'); }
    if (next.tenantIds.some(id => !hasID(c.tenantIds, id))) throw new Error('Refresh expanded workspaces; log in again.');
    if (hasID(next.tenantIds, c.tenantId)) next.tenantId = c.tenantId;
    await this.save(next);
    return next;
  }
  async api(method, endpoint, body) {
    const c = await this.ready();
    return this.request(c.baseUrl, method, `/skills${endpoint}`, { body: body === undefined ? undefined : stringifyJSON(body), contentType: body === undefined ? undefined : 'application/json', token: c.accessToken, tenant: this.selected(c) });
  }
  print(value) { this.out.write(`${stringifyJSON(value, 2)}\n`); }
  async login(flags) {
    const base = normalizeOrigin(flags['base-url'] ?? origins[this.profile]);
    const d = await this.form(base, '/oauth/device/code', { scope: flags['read-only'] ? 'flowboom.read flowboom.knowledge' : scopes });
    if (typeof d?.device_code !== 'string' || !d.device_code || !/^[A-Z2-7]{4}-[A-Z2-7]{4}$/.test(d.user_code) || !Number.isSafeInteger(d.expires_in) || d.expires_in < 1 || d.expires_in > 900 || !Number.isSafeInteger(d.interval) || d.interval < 1 || d.interval > d.expires_in) throw new Error('Invalid device authorization response.');
    let link;
    try { link = new URL(d.verification_uri_complete); } catch { throw new Error('Invalid verification URL.'); }
    if (link.origin !== base || link.username || link.password || link.hash || link.pathname !== '/oauth/device') throw new Error('Verification URL does not match selected site.');
    this.err.write(`Profile: ${this.profile}\nOpen: ${link.href}\nDevice code: ${d.user_code}\nChoose your workspaces and permissions in the browser. Waiting for authorization…\n`);
    if (!flags['no-browser']) this.openBrowser(link.href);
    const deadline = this.now() + d.expires_in * 1000;
    let interval = d.interval * 1000;
    while (this.now() < deadline) {
      await this.sleep(Math.min(interval, deadline - this.now()), undefined, { signal: this.signal });
      if (this.now() >= deadline) break;
      let c;
      try { c = await this.exchange(base, { grant_type: 'urn:ietf:params:oauth:grant-type:device_code', device_code: d.device_code }); }
      catch (error) {
        if (error instanceof APIError) {
          if (error.code === 'authorization_pending') continue;
          if (error.code === 'slow_down') { interval += 5000; continue; }
          if (error.code === 'access_denied') throw new Error('Authorization denied; no credentials saved.');
          if (error.code === 'expired_token') break;
        }
        throw error;
      }
      await this.request(c.baseUrl, 'GET', '/skills/identity', { token: c.accessToken, tenant: c.tenantId });
      await this.save(c);
      this.err.write(`Authorized default workspace ${c.tenantId} (${c.scope}). Credentials saved privately for profile ${this.profile}.\n`);
      return;
    }
    throw new Error('Device code expired; run flowboom auth login again.');
  }
  async auth(action, flags) {
    if (action === 'login') return this.login(flags);
    if (action === 'status') {
      const c = await this.ready();
      const data = await this.request(c.baseUrl, 'GET', '/skills/identity', { token: c.accessToken, tenant: this.selected(c) });
      this.print({ ...data, profile: this.profile, baseUrl: c.baseUrl, authorizedWorkspaceIds: c.tenantIds, defaultWorkspaceId: c.tenantId });
      return;
    }
    if (action === 'logout') {
      const c = await this.load();
      await this.form(c.baseUrl, '/oauth/revoke', { token: c.refreshToken }).catch(() => { throw new Error('Server revocation failed; local credentials retained. Retry logout or revoke in the browser.'); });
      await fs.unlink(this.credentialPath).catch(() => { throw new Error('Revoked on server but local credential cleanup failed.'); });
      this.err.write('Logged out; server authorization revoked.\n');
      return;
    }
    throw new Error('Use auth login, status or logout.');
  }
  async workspaces(action, id) {
    const c = await this.ready();
    if (action === 'list') { this.print(await this.request(c.baseUrl, 'GET', '/skills/workspaces', { token: c.accessToken })); return; }
    if (action !== 'use' || !id) throw new Error('Use workspaces list or workspaces use ID.');
    id = numericID(id);
    if (!hasID(c.tenantIds, id)) throw new Error('Workspace not authorized; log in again to add it.');
    await this.request(c.baseUrl, 'GET', '/skills/identity', { token: c.accessToken, tenant: id });
    c.tenantId = id;
    await this.save(c);
    this.err.write(`Default workspace set to ${id} for profile ${this.profile}.\n`);
  }
  async articleAction(action, id, confirm) {
    if (action === 'submit-review') { this.print(await this.api('POST', `/articles/${id}/submit-review`, {})); return; }
    const { data: article } = await this.api('GET', `/articles/${id}`);
    if (!article || String(article.id) !== id) throw new Error('Invalid article response.');
    const c = await this.load();
    const workspace = this.selected(c);
    if (!confirm) { this.print({ data: { profile: this.profile, workspaceId: workspace, articleId: article.id, siteId: article.siteId, title: article.title, status: article.status, confirmationRequired: true } }); return; }
    if (article.status !== 'ready') throw new Error('Article must pass review before publishing; approval remains in the web app.');
    this.err.write(`Publishing article ${id} in workspace ${workspace}, site ${article.siteId}.\n`);
    this.print(await this.api('POST', `/articles/${id}/publish`, { confirm: true }));
  }
  async run(args) {
    let flags, positionals;
    try { ({ values: flags, positionals } = parseArgs({ args, options, allowPositionals: true, strict: true })); }
    catch { throw new Error('Invalid command options; run flowboom help.'); }
    this.profile = flags.profile ?? this.env.FLOWBOOM_PROFILE ?? 'cn';
    if (!Object.hasOwn(origins, this.profile)) throw new Error('Profile must be cn or global.');
    this.workspace = flags.workspace === undefined ? undefined : numericID(flags.workspace);
    if (flags.version) { this.out.write('0.1.0\n'); return; }
    if (flags.help || !positionals.length || positionals[0] === 'help') { this.out.write(help); return; }
    const [command, action, rawID, ...extra] = positionals;
    if (extra.length) throw new Error('Unexpected arguments.');
    if (command === 'auth') {
      if (rawID) throw new Error('Unexpected auth arguments.');
      if (flags.confirm) throw new Error('--confirm is only valid for article publication or scheduling.');
      return this.auth(action, flags);
    }
    if (command === 'workspaces') {
      if ((action === 'list' && rawID) || !['list', 'use'].includes(action)) throw new Error('Use workspaces list or workspaces use ID.');
      return this.workspaces(action, rawID);
    }
    const requiresID = ['get', 'submit-review', 'publish'].includes(action);
    const id = requiresID && rawID ? positiveID(rawID) : undefined;
    if (rawID && !requiresID) throw new Error('Unexpected arguments.');
    if (command === 'articles' && ['submit-review', 'publish'].includes(action)) {
      if (!id) throw new Error('Article ID required.');
      return this.articleAction(action, id, !!flags.confirm);
    }
    if (flags.confirm && !(command === 'articles' && ['schedule', 'unschedule'].includes(action))) throw new Error('--confirm is only valid for article publication or scheduling.');
    const site = flags.site === undefined ? undefined : numericID(flags.site);
    let endpoint, method = 'GET', body;
    const query = new URLSearchParams();
    if (site !== undefined) query.set('siteId', String(site));
    const page = flags.page ?? '1', size = flags['page-size'] ?? '20';
    if (!/^[1-9]\d*$/.test(page) || !/^[1-9]\d*$/.test(size)) throw new Error('Page and page-size must be positive integers.');
    switch (command) {
      case 'sites': if (action !== 'list') throw new Error('Use sites list.'); endpoint = '/sites'; break;
      case 'sources': if (action !== 'list') throw new Error('Use sources list.'); endpoint = '/source-items'; query.set('page', page); if (flags.status) query.set('status', flags.status); break;
      case 'categories': if (action !== 'list' || !site) throw new Error('Use categories list --site ID.'); endpoint = '/categories'; break;
      case 'publish-tasks': if (action !== 'get' || !id) throw new Error('Use publish-tasks get ID.'); endpoint = `/publish-tasks/${id}`; break;
      case 'articles': case 'topics': case 'briefs':
        if (action === 'list') {
          endpoint = `/${command}`; query.set('page', page); query.set('pageSize', size);
          if (flags.query) query.set('q', flags.query); if (flags.status) query.set('status', flags.status);
        } else if (action === 'get' && id && command !== 'topics') endpoint = `/${command}/${id}`;
        else if (action === 'create' && command !== 'briefs') { method = 'POST'; endpoint = `/${command}`; }
        else if (action === 'from-knowledge' && command === 'briefs') { method = 'POST'; endpoint = '/knowledge/briefs'; }
        else if (command === 'articles' && ['schedule', 'unschedule'].includes(action)) { method = 'POST'; endpoint = `/articles/${action}`; }
        else if (command === 'articles' && action === 'plan' && site) { endpoint = '/publication-plan'; query.set('page', page); query.set('pageSize', size); }
        else throw new Error('Unknown content command; run flowboom help.');
        break;
      case 'knowledge':
        if (action !== 'search' || !flags.query?.trim()) throw new Error('Use knowledge search --query TEXT [--site ID].');
        method = 'POST'; endpoint = '/knowledge/search'; body = { query: flags.query, siteId: site ?? 0 }; break;
      case 'diagnostics': if (action) throw new Error('Use diagnostics [--site ID].'); endpoint = '/diagnostics'; break;
      case 'seo':
        if (!site || !['report', 'opportunities'].includes(action)) throw new Error('Use seo report or seo opportunities --site ID.');
        endpoint = `/sites/${site}/seo/${action === 'report' ? 'overview' : 'opportunities'}`; query.delete('siteId');
        if (action === 'report') { query.set('engine', flags.engine ?? 'google'); query.set('source', flags.source ?? 'ga4'); if (flags.from) query.set('from', flags.from); if (flags.to) query.set('to', flags.to); }
        else if (flags.status) query.set('status', flags.status);
        break;
      default: throw new Error('Unknown command; run flowboom help.');
    }
    if (method === 'POST' && body === undefined) {
      if (!flags.file) throw new Error('--file is required.');
      try {
        const file = await fs.open(flags.file, 'r');
        try { if (!(await file.stat()).isFile() || (await file.stat()).size > MAX_FILE) throw new Error('size'); body = parseJSON(await file.readFile('utf8')); }
        finally { await file.close(); }
      } catch { throw new Error('Request file must contain valid JSON, at most 1 MiB.'); }
      if (!body || typeof body !== 'object' || Array.isArray(body)) throw new Error('Request file must contain a JSON object.');
    }
    if (command === 'articles' && ['schedule', 'unschedule'].includes(action)) body.confirm = !!flags.confirm;
    if (method === 'GET' && query.size) endpoint += `?${query}`;
    this.print(await this.api(method, endpoint, body));
  }
}

export const help = `Flowboom — content operations for Codex and WorkBuddy

flowboom [--profile cn|global] [--workspace ID] COMMAND

Profiles: cn=https://cloud.flowboom.cn (default)
          global=https://cloud.flowboom.io (deployment pending)

  auth login [--no-browser] [--read-only] [--base-url HTTPS_ORIGIN]
  auth status | auth logout
  workspaces list | workspaces use ID
  sites list
  sources list [--site ID] [--page N] [--status STATUS]
  categories list --site ID
  articles list [--site ID] [--status STATUS] [--query TEXT] [--page N]
  articles get ID
  articles create --file article.json
  articles submit-review ID
  articles publish ID [--confirm]
  articles schedule --file plan.json [--confirm]
  articles unschedule --file selection.json [--confirm]
  articles plan --site ID [--page N]
  publish-tasks get ID
  topics list [--site ID] [--status STATUS]
  topics create --file topic.json
  briefs list [--site ID] [--page N]
  briefs get ID
  briefs from-knowledge --file brief.json
  knowledge search --query TEXT [--site ID]
  diagnostics [--site ID]
  seo report --site ID [--from DATE --to DATE --engine google|bing|baidu --source ga4|baidu_tongji]
  seo opportunities --site ID [--status STATUS]

API results are JSON. Tokens are never printed. Writes are never retried.
Publish/schedule/unschedule without --confirm only previews the exact targets.
Scheduling uses the site's enabled publish_sweep pipeline; due articles publish on its next run.
Canceling a ready article plan returns it to draft to prevent unintended automatic publication.
Review approval, paid generation and customer outreach remain in the web app.
`;
