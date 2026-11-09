# Flowboom CLI & Skills

Open-source client and six portable agent skills for Flowboom content operations. Requires Go 1.24+ to build; the binary has no runtime dependencies.

## Install and connect

```sh
go install github.com/GigaValue/flowboom-cli@latest
# The installed executable is flowboom-cli. Use it directly, or build flowboom:
git clone https://github.com/GigaValue/flowboom-cli.git
cd flowboom-cli
go build -o bin/flowboom .
export PATH="$PWD/bin:$PATH"
flowboom auth login
flowboom auth status
flowboom sites list
```

Login opens the Flowboom browser page, where you sign in, compare the device code, select a workspace and approve optional knowledge/draft permissions. Passwords and captcha stay in the browser. For an agent/remote terminal, use `flowboom auth login --no-browser` and open the printed URL yourself. Do not put passwords or tokens into prompts.

**Deployment prerequisite:** the selected Flowboom site must deploy the device OAuth and Skills API documented in [the protocol](docs/api.md). This repository does not deploy those services. A site that lacks them will return an error rather than pretend login succeeded.

## China and international profiles

```sh
flowboom --profile cn auth login       # https://cloud.flowboom.cn; default
flowboom --profile global auth login   # https://cloud.flowboom.io; deployment pending
flowboom --profile global sites list
```

`FLOWBOOM_PROFILE=cn|global` sets the default. Each profile stores its own server origin, tokens, granted scope and single authorized workspace. Normal commands and refresh use that stored origin; switching profiles never sends the other site's tokens. Accounts and workspace IDs are independent between sites; no shared login/SSO is assumed. To change workspace, log in again in that profile. For a self-hosted server, pass `auth login --base-url https://your-flowboom.example`; loopback HTTP is accepted for local demos only. International configuration support is not a claim that the international service is deployed.

Credentials live in the OS user configuration directory (`flowboom/cn.json` and `flowboom/global.json`), outside the repository. Unix permissions are 0700 for the directory and 0600 for the files. Refresh rotates credentials within an absolute seven-day session. Run `flowboom auth logout` to revoke the server session and remove local credentials. Browser `/oauth/device` also lists and revokes active authorizations.

## Install skills

After building/installing the CLI, install these directories with your agent's skill installer:

- `flowboom-write-article`: write directly and save an article draft.
- `flowboom-content-inventory`: inventory content states and operational backlog.
- `flowboom-seo-review`: review search/traffic coverage and optimization opportunities.
- `flowboom-plan-topics`: plan topics from actual source titles and existing content.
- `flowboom-knowledge-brief`: turn enabled knowledge paragraphs into a traceable draft Brief.
- `flowboom-publishing-check`: diagnose generation/publishing state without rerunning jobs.

For Codex local discovery, copy the directories in `skills/` into `~/.agents/skills/` or the target project's `.agents/skills/`. Preserve any existing skill with the same name; compare/update explicitly instead of overwriting it. For WorkBuddy, import each directory through the skill import mechanism available in your version and enable local command execution. The workflow files use `SKILL.md`; no WorkBuddy-specific installer path is assumed or yet verified.

The skills call the shared `flowboom` command. If using the executable installed by `go install`, use `flowboom-cli` wherever instructions say `flowboom`. Skills do not require this checkout once imported; each contains its needed API usage reference.

## Example: write an article directly

Ask the agent: “参考企业知识库，写一篇产品选型文章，存为中文站草稿。” The agent checks your profile/site, gathers relevant evidence, writes Markdown itself, then creates a draft:

```sh
flowboom articles create --file article.json
```

```json
{"siteId":1,"title":"产品选型指南","slug":"product-selection-guide","contentMarkdown":"# 产品选型指南\n\n根据已核实资料撰写的正文。"}
```

Article IDs and site IDs above are placeholders. Direct drafting uses your agent's own model, not Flowboom's paid generation queue. Publishing/review and paid task execution stay in the web app. A timeout may still mean a draft was saved: inspect the matching title/slug before resubmitting; the CLI never retries a write automatically.

## Verify

```sh
go test ./...
go vet ./...
go build -o bin/flowboom .
```

Tests use synthetic local HTTP servers and temporary credential directories. They do not contact either production site, invoke paid models or modify real content. See [API contract](docs/api.md) for deployment and permission details.
