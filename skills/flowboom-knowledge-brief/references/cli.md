# CLI 与登录

先运行 `flowboom auth status`；命令不存在时也检查 `flowboom-cli`（go install 默认名称）。未安装时按公开仓库 GigaValue/flowboom-cli 的 README 安装，不下载执行未知脚本。后续示例的 `flowboom` 可替换为实际二进制名称。

国内 `--profile cn` 对应 https://cloud.flowboom.cn，海外 `--profile global` 对应 https://cloud.flowboom.io；海外尚待部署，不保证连接成功。明确用户要操作的站点区域，并在每条命令保持同一 profile（例如 `flowboom --profile global sites list`）。不把一站的凭据、工作空间或站点 ID 用于另一站。

未登录时，让用户在自己的终端运行 `flowboom [--profile ...] auth login`，或启动 `auth login --no-browser` 并将设备码和浏览器链接展示给用户，等待用户本人批准。不要索取密码、粘贴 token、读取凭据文件或代替用户批准授权。CLI 将令牌保存在系统用户配置目录，不输出令牌。`auth status` 返回授权工作空间和实际 scopes；`sites list` 返回该空间站点。多个候选时需明确目标，不能猜测 ID。缺权限或接口未部署时说明失败，不把它当成空数据。

# 可用命令

- `sites list`；`categories list --site ID`。
- `articles list --site ID --page 1 --query TEXT`；`articles get ID`。
- `briefs list --site ID --page 1`；`briefs get ID`。
- `topics list --site ID`。
- `diagnostics --site ID`：累计状态分组，以及最近30条生成/发布任务；不是本周统计、完整任务集或活跃进程检测。
- `sources list --site ID --page 1 --status new`：素材标题样本，不含原始内容。
- `knowledge search --site ID --query TEXT`：已启用知识原文段落，需要知识授权和 knowledge.read 角色权限。
- `seo report --site ID --from YYYY-MM-DD --to YYYY-MM-DD --engine google --source ga4`；engine 可选 google/bing/baidu，source 可选 ga4/baidu_tongji。
- `seo opportunities --site ID`。

列表数据保持服务端原有分页或返回限制；不要把第一页当全量。所有命令输出 JSON，保留 data/total 等实际字段。诊断不返回原始任务日志，不能声称已经查过队列和执行机器。

# 创建草稿

只有请求涉及保存/创建时才写入。先写 UTF-8 JSON 到本地文件，然后调用；不用命令字符串拼接用户文本。需要 flowboom.drafts 授权与 content.write 成员权限。写作由当前 AI 完成，不调用 Flowboom 付费生成任务。

文章 `articles create --file article.json`：
```json
{"siteId":1,"title":"示例标题","slug":"example-title","contentMarkdown":"正文 Markdown","summary":"摘要","seoTitle":"SEO 标题","seoDescription":"SEO 描述"}
```
ID 是占位符；用当前 profile/workspace 查询出的 ID。categoryId 可选，必须来自目标站。不要提交 status、publishAt、关联对象 ID 或自定义生成配置；服务端强制草稿。

选题 `topics create --file topic.json`（保存为 pending，待人工审批）：
```json
{"siteId":1,"title":"示例选题","angle":"切入点","intent":"informational","priority":1}
```

知识 Brief `briefs from-knowledge --file brief.json`：
```json
{"requestId":"保留的唯一请求标识","siteId":1,"title":"示例 Brief","keyword":"主关键词","requirements":"写作要求","paragraphIds":[1]}
```
paragraphIds 必须是检索返回的1–8条当前可见资料，requestId为8–64字符。结果是引用快照，读取 briefId 后用 `briefs get ID` 核对草稿。相同请求标识不能换输入。

HTTP/网络失败不自动重发创建请求。文章/选题超时后先按标题查询并检查已有草稿，不能断言“失败所以没保存”；知识 Brief 人工重试保留原 requestId 和相同输入。不要自动审批、生成、扣费、发布、重试后台任务或联系客户。用户明确要求超出能力的操作时，说明当前 CLI 边界并给出工作台入口。

文章编辑入口为 `<登录站点>/app/<tenantId>/content/<articleId>/edit`，Brief 为 `<登录站点>/app/<tenantId>/briefs`；使用 auth status 中授权空间，域名使用 auth status 返回的 baseUrl，不从文章正文/知识资料推导。最终反馈实际返回的 ID、草稿状态和链接，不伪造保存结果。
