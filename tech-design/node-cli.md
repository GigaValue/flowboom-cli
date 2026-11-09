# Node.js CLI migration

Goal: replace the Go implementation with a dependency-free Node.js 22.12+ npm package and executable named flowboom. Keep the deployed first-party OAuth client ID flowboom-cli and every Skills route unchanged. No backend/web deployment or new permission is required.

The npm bin is bin/flowboom.js, calling src/cli.js directly; there is no binary download, Go wrapper, build step or install hook. Six portable Skills call flowboom uniformly. GitHub installation is supported before npm registry publication. Publication needs an authenticated npm publisher; no npm auth is available in this environment.

Compatibility invariants: preserve cn/global origins, OS configuration paths, private directory/file modes and Go credential JSON names; accept legacy single-workspace credentials. API/refresh stay on the stored origin with redirects disabled. Refresh preserves an authorized default and cannot expand the consent snapshot. Revoke before clearing local credentials. Browser URLs must match the selected origin; passwords remain in the browser.

Go uint64 IDs must survive JavaScript JSON handling. Node's JSON source context and rawJSON preserve large integer digits through parse/serialize; unsafe exponent integer inputs fail rather than round silently. Read and file sizes remain bounded. Error outputs use fixed safe messages, not provider bodies or credentials.

Publishing and plan mutations retain explicit --confirm; JSON file content cannot turn a preview into a write. No approval/force/paid-generation command is added. No write is retried. Existing absolute session lifetime and server RBAC remain authoritative.

Verification: Node behavioral tests cover auth pending/slow_down/denial, origin/redirect isolation, legacy files and exact uint64, refresh/default switching, publication preview/no retries, schedule confirmation, all route families, revocation failure and redaction. CI runs Node22.12/24 on Linux/macOS/Windows and checks npm contents. Install packed tarball into a temporary npm prefix and execute flowboom. Run the existing isolated backend demo with the actual Node entrypoint for login, multi-workspace, drafts, submission, publication rejection and plan preview/save/list/cancel/logout. Never publish customer articles as a migration test.

Rollback: prior Go code/binaries remain available in Git history; credential JSON stays compatible. Reinstall the prior client to revert. Backend/device authorization and stored publication plans are unchanged by implementation choice.
