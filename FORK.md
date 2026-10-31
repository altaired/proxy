# Fork notes

This repository is a fork of [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). Fork features are kept out of upstream files so syncing stays conflict-free.

## Keeping the fork syncable

- Put fork code in new files. Upstream rewrites its core files almost daily, so every edited line there is a future merge conflict.
- When an upstream file must change, keep the hook to a few lines and list it under [Upstream touch points](#upstream-touch-points).
- Document fork features here instead of in `config.example.yaml` or `README.md`, which upstream changes constantly.

Sync with upstream:

```bash
git remote add upstream https://github.com/router-for-me/CLIProxyAPI.git  # once
git fetch upstream
git merge upstream/main
go build ./... && go test ./...
```

A compile error after a sync usually means upstream renamed a helper that a fork file calls; the tests for each feature below cover its behavior.

## Features

### Quota-aware credential routing

```yaml
routing:
  strategy: "quota-aware"
  session-affinity: true
```

`quota-aware` spends Claude and Codex subscription quota before it resets. Among subscriptions with headroom it uses the one whose weekly window resets soonest, until that one runs out. It ranks from the quota headers each response already carries (`Anthropic-Ratelimit-Unified-*`, `X-Codex-*`).

| Credential | Order |
| --- | --- |
| Subscription without usage data yet (for example after a restart) | First, in round-robin, until a response reports its usage |
| Subscription with headroom | By soonest weekly reset |
| API keys and providers that do not report usage | Round-robin |
| Subscription with a used-up 5h or weekly window | Last |

Credential priority tiers still apply first. With `session-affinity`, ongoing sessions stay on their subscription so prompt caching (which is per account) keeps working; only new sessions follow the ranking. A bound session stays on its subscription until the upstream rejects it, so an account with paid overage enabled keeps serving that session even when another subscription still has headroom.

Code: `sdk/cliproxy/auth/quota_routing.go`, tests in `sdk/cliproxy/auth/quota_routing_test.go` and `sdk/cliproxy/service_config_quota_test.go`.

## Upstream touch points

| File | Change |
| --- | --- |
| `sdk/cliproxy/service_config.go` | Maps `routing.strategy: quota-aware` to `QuotaAwareSelector` |
