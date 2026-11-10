# Device OAuth and Skills API contract

Paths are relative to `/api/v1` on the selected HTTPS origin. First-party public client ID: `flowboom-cli`; no client secret. The Node.js executable is `flowboom`; renaming the command does not change the registered OAuth client ID. Both cn and global must implement the same contract; account, workspace and credential stores remain independent.

## Login

1. Form POST `/oauth/device/code`: `client_id`, `scope`. Returns `device_code`, `user_code`, `verification_uri`, `verification_uri_complete`, `expires_in` (600 seconds), `interval` (5 seconds).
2. Browser opens `/oauth/device`, signs in through normal web login and completes captcha if configured. Compare the displayed code to your own terminal. Only approve requests you initiated.
3. Existing browser JWT authorizes JSON POST `/oauth/device/inspect` (`user_code`) and `/oauth/device/decision` (`user_code`, `tenant_ids` (explicit current-workspace ID snapshot; legacy `tenant_id` accepted), approved `scope`, `approve`). Workspace selection and optional scopes require explicit input. A denial requires no workspace.
4. CLI polls form POST `/oauth/token` with `client_id`, `grant_type=urn:ietf:params:oauth:grant-type:device_code`, `device_code`. Honor `authorization_pending`, `slow_down` (+5 seconds), `access_denied`, `expired_token`.
5. Token response: `access_token`, `refresh_token`, `token_type=Bearer`, `expires_in`, `scope`, `tenant_id` (default), `tenant_ids` (currently active consented IDs). Access expires after at most one hour; session after seven days. Refresh with `grant_type=refresh_token`, `refresh_token`, `client_id`; both tokens rotate. A lost token response requires a new login.
6. Form POST `/oauth/revoke` with `client_id`, `token` revokes that session. Browser JWT GET `/oauth/sessions` and DELETE `/oauth/sessions/:id` manage the user's own sessions.

Scopes: `flowboom.read` (content/SEO/status reads), `flowboom.knowledge` (knowledge search), `flowboom.drafts` (draft creation), `flowboom.publish` (submit review and publish a reviewed ready article). They do not grant additional membership/RBAC permissions. Tokens bind a snapshot of explicitly consented workspaces; new memberships are excluded. `GET /skills/workspaces` lists only active consented IDs/names and needs no tenant header. Other Skills requests carry `Authorization: Bearer ...` and `X-Tenant-ID`; IDs outside the snapshot fail. Removal of one membership blocks that workspace immediately while other authorized workspaces remain usable; refresh returns and persists only currently active consented IDs. Memberships removed at refresh require fresh consent if subsequently restored. Device access is checked against live membership/account state and revocation. Device tokens are rejected by existing JWT business/admin APIs.

## Content operations

| Method/path under `/skills` | Scope | Result |
|---|---|---|
| GET `/workspaces` | identity | active consented workspace IDs/names; no tenant header |
| POST `/articles/:id/submit-review` | articles.submit-review | draft/rejected → reviewing; content.write RBAC |
| POST `/articles/:id/publish` | articles.publish | `{confirm:true}` → async publish task; ready only, SEO/content-pack gates, content.write RBAC; no force |
| GET `/publish-tasks/:id` | read | safe task identity/state/timestamps; no raw logs/errors |
| GET `/identity` | identity | user ID, authorized tenant and scopes |
| GET `/sites` | identity | site ID, name, domain, language, engine, status; no deployment settings |
| GET `/categories?siteId=ID` | read | category identity, no generation credentials |
| GET `/articles?siteId=ID&page=1&pageSize=20&q=TEXT&status=draft` | read | paged articles (`data`, `total`, `page`, `pageSize`) |
| GET `/articles/:id` | read | one article |
| GET `/briefs?siteId=ID&page=1&pageSize=20` | read | paged Brief summaries; fetch detail before reasoning about requirements |
| GET `/briefs/:id` | read | Brief detail excluding raw source/prompt fields |
| GET `/topics?siteId=ID&status=draft` | read | existing topic list |
| GET `/source-items?siteId=ID&page=1&status=new` | read | source title sample, total; excludes raw payload |
| GET `/diagnostics?siteId=ID` | read | cumulative status counts and 30 most recent generation/publish task projections; no logs |
| GET `/sites/:id/seo/overview?from=DATE&to=DATE&engine=google&source=ga4` | read | real existing report, including sync coverage |
| GET `/sites/:id/seo/opportunities?status=STATUS` | read | existing opportunities |
| POST `/knowledge/search` | read + knowledge | `{query,siteId}` → enabled knowledge paragraph hits; requires knowledge.read RBAC |
| PATCH `/articles/:id` | articles.update | supplied editorial fields only; unpublished draft/rejected, content.write RBAC; revision and SEO updated atomically |
| POST `/articles` | articles.create | article draft only; content.write RBAC |
| POST `/topics` | articles.create | manual pending topic only; content.write RBAC |
| POST `/knowledge/briefs` | knowledge + articles.create | traceable draft Brief; knowledge.read/content.write RBAC |

Scope names in the table use the `flowboom.` prefix. Legacy drafts/publish remain accepted on their original routes. Granular writes do not require content read; knowledge search retains read/knowledge requirements, and Brief creation requires knowledge plus creation authorization.

Article draft JSON: required `siteId`, `title` (≤200 characters), `slug` (≤255 bytes); optional `categoryId`, `summary`, `contentMarkdown`, `seoTitle`, `seoDescription`. Topic JSON: required `siteId`, `title`; optional `categoryId`, `angle`, `intent`, `priority`. Article status is server-owned `draft`; topic status is server-owned `pending`, compatible with the existing approval workflow. Linked object fields, scheduled publishing, custom prompts and workflow transitions are not accepted. Site/category ownership must match the authorized workspace.

Knowledge Brief JSON: `requestId` (8–64 characters; retain across transport retries), `siteId`, `title`, `keyword`, `requirements` (≤5000 characters), `paragraphIds` (1–8 enabled current paragraphs). The response is a reference snapshot containing `briefId`, not a generation task. The same requestId with changed inputs is rejected.

Responses retain the existing `{data:...}` envelope and pagination metadata. 401 means invalid/expired/revoked authorization; 403 means workspace, scope or role denied. Do not report permission failure as empty business data. Server bodies/logs may contain operational details: the CLI prints only stable errors, not raw error bodies. POSTs are not automatically retried. Publication needs explicit publish scope, confirmation and an approved article; no review-approval endpoint exists. Existing grants do not gain publish scope. Nothing here grants paid generation, billing, contacts or platform administration. Repeat publication of an in-flight article is rejected; ambiguous enqueue failure retains task evidence for recovery via the web workflow.

## Batch article scheduling

- POST `/skills/articles/schedule`: `{siteId,articleIds,startAt,intervalDays,perDay,confirm}`. 1–100 unique article IDs in requested order; future RFC3339 startAt with offset; intervalDays1–365, perDay1–20. All selected articles must belong to the site/workspace and be ready SEO content. `confirm:false` previews without writes; true atomically updates publish_at after validation, synchronizing linked topic due_at. Requires articles.schedule scope/content.write and an enabled site publish_sweep pipeline for confirmed save. Does not grant approval or enable pipelines.
- POST `/skills/articles/unschedule`: `{siteId,articleIds,confirm}`. Preview first; true clears publish_at and linked topic due_at, moving ready articles to draft so an automatic sweep cannot immediately pick them up. Publishing/published states are rejected.
- GET `/skills/publication-plan?siteId=ID&page=1&pageSize=20`: read scope; safe article identities, titles, status, publication dates and enabled publish_sweep IDs/names/cron expressions; no recipe config, provider errors or credentials.

Responses identify `execution=next_enabled_publish_sweep` and `scheduleTimezone=Asia/Shanghai`, matching the existing worker. Date batches follow UTC-normalized time with fixed day intervals. No independent exact-time job is created; due articles follow existing enabled pipeline/worker behavior, so outage or disabling the pipeline delays execution. Persisted plans do not depend on CLI login remaining active. No customer publication is performed in verification.

## Node.js client compatibility

The CLI runs on Node.js 22.12+ as `flowboom`, with no runtime npm dependencies. The HTTP contract, OAuth client ID `flowboom-cli`, profile names and credential JSON fields are unchanged by the implementation migration. Existing private Go CLI credentials remain readable at the same OS paths; refresh and revocation still use the login origin. The npm package name and its `bin` command are both `flowboom`; the repository and OAuth client ID remain `flowboom-cli`.

## Granular authorization and unattended agents

New browser login requests independent permissions. Content read is optional for a grant containing an individual write permission. Existing legacy grants retain their original rights and never gain published revision or agent review rights automatically. Re-login to change consent. Site discovery and identity do not grant content read.

PATs are created in Settings for one workspace and one active site. Choose only necessary permissions; the secret is displayed once, stored hashed by the server, expires after 1–365 days and can be revoked with its audit history visible. PATs do not refresh. Replace before expiry, then revoke the old token. Membership, account and role checks remain live. PATs cannot access knowledge or management APIs.

For a secret injected by your process manager, set `FLOWBOOM_PAT`, `FLOWBOOM_BASE_URL` and `FLOWBOOM_WORKSPACE` together. The explicit HTTPS origin prevents sending the secret to another profile. Environment credentials are not saved. Alternatively pipe a protected secret file into `flowboom --workspace ID auth login --pat-stdin --base-url HTTPS_ORIGIN`; never paste the secret into a command argument or an AI prompt. This stores a private credential file without a refresh token. `auth logout` revokes the PAT; also remove any injected environment secret.

## Updating published articles

Hexo/Astro standalone articles support a separate revision workflow:

```sh
flowboom articles get ARTICLE_ID
flowboom articles revise ARTICLE_ID --file revision.json
flowboom articles revisions ARTICLE_ID
flowboom revisions review REVISION_ID --confirm
flowboom revisions get REVISION_ID
flowboom revisions publish REVISION_ID --confirm
flowboom publish-tasks get TASK_ID
```

`revision.json` contains `expectedRevision` from the article, changed `title`, `summary`, `contentMarkdown`, `seoTitle` or `seoDescription`, and required `evidence` explaining factual sources. Site, slug and category cannot change. Complete baseline, candidate and evidence must fit 50KB; large inputs fail rather than silently truncate. Creation leaves the master article unchanged. WordPress, content packs and managed translations use their existing workflows.

Permissions are independent: `flowboom.articles.revise`, `flowboom.articles.agent-review`, `flowboom.articles.publish`; reads require `flowboom.read`. Agent review also needs `assistant.use` and `content.write`. Review uses the platform agent and may incur its normal model usage. Callers cannot submit a verdict, run ID or force flag. Only a successful review matching the exact candidate can approve it; factual, readability or safety holds block publication. A changed baseline also blocks publication. Review/publish without `--confirm` only previews the requested action and performs no write.

After confirmation, candidate changes and the existing publish task are saved atomically. A queued task is not an online result. Deployment failure uses existing retry/recovery behavior; the original snapshot remains available for operator recovery, with no automatic deployment rollback guarantee. Do not retry ambiguous writes blindly.

Rollout requires the matching backend, migrations 000079/000080, browser build before using the new CLI defaults. Older servers may reject granular scope requests.
