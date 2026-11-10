# Flowboom CLI & Skills

Open-source Node.js client and six portable agent skills for Flowboom content operations. Requires Node.js 22.12 or newer; no runtime dependencies or build step.

## Install and connect

Install from the public GitHub repository:

```sh
npm install -g github:GigaValue/flowboom-cli
flowboom auth login
flowboom auth status
flowboom sites list
```

Or run without a global installation:

```sh
npm exec --package github:GigaValue/flowboom-cli -- flowboom auth login
```

The executable is always `flowboom`, including on Windows. npm registry publication is pending; after `flowboom` is published, the equivalent commands are `npm install -g flowboom` and `npx flowboom`. For a local checkout, use `npm link` or `node bin/flowboom.js help`.

Login opens the Flowboom browser page, where you sign in, compare the device code, select workspaces and independently approve read, knowledge, create, edit, revise, agent review, submit review, publish and schedule permissions. Passwords and captcha stay in the browser. For an agent/remote terminal, use `flowboom auth login --no-browser` and open the printed URL yourself. Do not put passwords or tokens into prompts.

**Deployment prerequisite:** the selected Flowboom site must deploy the device OAuth and Skills API documented in [the protocol](docs/api.md). This repository does not deploy those services. A site that lacks them will return an error rather than pretend login succeeded.

## China and international profiles

```sh
flowboom --profile cn auth login       # https://cloud.flowboom.cn; default
flowboom --profile global auth login   # https://cloud.flowboom.io; deployment pending
flowboom --profile global sites list
```

`FLOWBOOM_PROFILE=cn|global` sets the default. Each profile stores its own server origin, tokens, granted scope and explicitly authorized workspace snapshot. Normal commands and refresh use that stored origin; switching profiles never sends the other site's tokens. Accounts and workspace IDs are independent between sites; no shared login/SSO is assumed. Use `workspaces list`, `workspaces use ID` to save a default, or `--workspace ID` for one command. Newly joined workspaces require fresh consent. For a self-hosted server, pass `auth login --base-url https://your-flowboom.example`; loopback HTTP is accepted for local demos only. International configuration support is not a claim that the international service is deployed.

Credentials live in the OS user configuration directory (`flowboom/cn.json` and `flowboom/global.json`), outside the repository. The paths and JSON format remain compatible with the previous Go CLI: Linux uses `$XDG_CONFIG_HOME` or `~/.config`, macOS uses `~/Library/Application Support`, and Windows uses `%APPDATA%`. Existing private credentials can be reused without copying tokens. Unix permissions are 0700 for the directory and 0600 for the files. Refresh rotates credentials within an absolute seven-day session. Run `flowboom auth logout` to revoke the server session and remove local credentials. Browser `/oauth/device` also lists and revokes active authorizations.

## Install skills

After installing the CLI, install these directories with your agent's skill installer:

- `flowboom-write-article`: write directly and save an article draft.
- `flowboom-content-inventory`: inventory content states and operational backlog.
- `flowboom-seo-review`: review search/traffic coverage and optimization opportunities.
- `flowboom-plan-topics`: plan topics from actual source titles and existing content.
- `flowboom-knowledge-brief`: turn enabled knowledge paragraphs into a traceable draft Brief.
- `flowboom-publishing-check`: inspect publication state and explicitly submit, publish or schedule reviewed articles.

For Codex local discovery, copy the directories in `skills/` into `~/.agents/skills/` or the target project's `.agents/skills/`. Preserve any existing skill with the same name; compare/update explicitly instead of overwriting it. For WorkBuddy, import each directory through the skill import mechanism available in your version and enable local command execution. The workflow files use `SKILL.md`; no WorkBuddy-specific installer path is assumed or yet verified.

The skills call the shared `flowboom` command. Skills do not require this checkout once imported; each contains its needed API usage reference.

## Example: write an article directly

Ask the agent: “参考企业知识库，写一篇产品选型文章，存为中文站草稿。” The agent checks your profile/site, gathers relevant evidence, writes Markdown itself, then creates a draft:

```sh
flowboom articles create --file article.json
```

```json
{"siteId":1,"title":"产品选型指南","slug":"product-selection-guide","contentMarkdown":"# 产品选型指南\n\n根据已核实资料撰写的正文。"}
```

Article IDs and site IDs above are placeholders. Direct drafting uses your agent's own model, not Flowboom's paid generation queue. Review approval and paid task execution stay in the web app; explicit publication and planning are available below. A timeout may still mean a draft was saved: inspect the matching title/slug before resubmitting; the CLI never retries a write automatically.

## Verify

```sh
npm test
npm run check
npm pack --dry-run
```

Tests use synthetic local HTTP servers and temporary credential directories. They do not contact either production site, invoke paid models or modify real content. See [API contract](docs/api.md) for deployment and permission details.

## Multiple workspaces and publication

Browser consent can select multiple workspaces or **all current workspaces**. New memberships are not automatically included. Every request checks live membership and the selected workspace role. Removing one workspace does not disable access to the remaining authorized workspaces.

```sh
flowboom workspaces list
flowboom workspaces use 12
flowboom --workspace 12 articles submit-review 34
# Approval remains in the web app.
flowboom --workspace 12 articles publish 34           # preview only
flowboom --workspace 12 articles publish 34 --confirm # queue a reviewed ready article
flowboom --workspace 12 publish-tasks get 56
```

Submission/publication requires separately checked `flowboom.articles.submit-review` / `flowboom.articles.publish` browser consent (legacy `flowboom.publish` remains valid) and existing `content.write` permission. Existing tokens never gain this scope automatically. Re-login to grant it. Publication retains SEO/content-pack review checks and does not offer force or approval commands. A returned task means queued, not published. Writes are not retried; inspect task/article state after an ambiguous failure and use the web workflow for recovery. Legacy single-workspace credentials remain usable.

## Batch publication plans

For already reviewed articles, prepare `plan.json` with `{ "siteId": 1, "articleIds": [11,12], "startAt": "2030-01-01T09:00:00+08:00", "intervalDays": 1, "perDay": 5 }` (replace example IDs/date).

```sh
flowboom --workspace 12 articles schedule --file plan.json           # preview only
flowboom --workspace 12 articles schedule --file plan.json --confirm
flowboom --workspace 12 articles plan --site 1
flowboom --workspace 12 articles unschedule --file selection.json    # preview only
```

A plan accepts 1–100 ready SEO articles in the provided order, with 1–20 per batch and 1–365 days between batches. Time must include an explicit timezone and be in the future. Saving requires an enabled site `publish_sweep` pipeline; execution occurs on its next run after each due time, not at an independent exact timer. Existing cron uses Asia/Shanghai. The CLI does not enable site-wide automation. Plans persist after logout. Cancellation (`selection.json`: `siteId`, `articleIds`, add `--confirm` to apply) clears dates and returns ready articles to draft to prevent the next sweep from publishing them immediately; reapproval is needed afterward. Publishing/published articles cannot be canceled this way.

## Edit a draft and upgrade

```sh
flowboom articles update ARTICLE_ID --file changes.json
flowboom upgrade
```

`changes.json` contains only fields to change, for example:

```json
{"title":"Updated title","contentMarkdown":"# Updated article","summary":""}
```

Editable fields: title, slug, summary, contentMarkdown, seoTitle, seoDescription and categoryId. Omitted/null fields stay unchanged; empty strings clear optional text. Only unpublished draft/rejected articles can be edited. Reviewed, reviewing, publishing and published articles are rejected. Managed translations must be edited in the web app. `flowboom.articles.update` (or legacy draft scope) and content.write permission are required; changes never grant approval. Editing requires a server version that exposes the draft PATCH endpoint.

`flowboom upgrade` runs `npm install --global github:GigaValue/flowboom-cli` using npm's configured global prefix. Use it for a global npm installation; local/npx/source checkouts should be updated through their original installation method. npm must be available and its global directory writable. No sudo or credential reset is performed.

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
