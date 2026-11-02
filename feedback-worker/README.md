# QKA Feedback Worker

Small Cloudflare Worker proxy for the in-app feedback dialog. The desktop
client never receives a GitHub token. The Worker validates and rate-limits the
request, commits images to the `feedback-assets` branch in one Git tree commit,
then creates an Issue in `sljdxde/quota-keepalive`.

## Secrets

```sh
npx -y wrangler@latest secret put GITHUB_TOKEN --config feedback-worker/wrangler.toml
npx -y wrangler@latest secret put SITE_KEY --config feedback-worker/wrangler.toml
```

`SITE_KEY` must match the default site key compiled into the desktop client.
It is an anti-crawler gate, not a GitHub authorization credential.

## Verify and deploy

```sh
node --test feedback-worker/src/index.test.js
npx -y wrangler@latest deploy --config feedback-worker/wrangler.toml
```
