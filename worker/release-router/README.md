# open-platform-cli release router worker

This Cloudflare Worker gives `avtkit` installation docs a stable URL while still resolving the latest GitHub Release at request time.

## What it serves

- `GET /install.sh`: returns a bootstrap shell script for fresh installs.
- `GET /upgrade.sh`: returns the same bootstrap flow with upgrade-oriented logging.
- `GET /latest`: returns JSON metadata for the latest GitHub release plus the stable worker URLs.
- `GET /releases/latest/download/:os/:arch`: resolves the latest GitHub release, finds the matching asset for the requested platform, and redirects to the GitHub asset download URL.
- `GET /healthz`: lightweight health check.

Example stable docs entrypoints:

```bash
curl -fsSL https://cli-install.example.com/install.sh | sh
curl -fsSL https://cli-install.example.com/upgrade.sh | sh
```

## Why this shape

The Worker owns the stable public URLs. The bootstrap script then downloads the latest release archive through the Worker again, so the final binary target can move with each GitHub release without changing the docs.

This keeps the MVP simple:

- no separate storage bucket
- no need to publish `install.sh` as a release asset first
- easy to hotfix bootstrap logic independently from the Go release artifacts
- ready to plug into a later GitHub Release workflow as long as release assets follow a predictable naming contract

## Expected release asset contract

The Worker currently looks for assets whose filenames include:

- the CLI name: `avtkit`
- the OS token: `darwin`, `linux`, or `windows`
- the architecture token: `amd64` or `arm64`
- a supported archive suffix

Unix assets should end with `.tar.gz` or `.tgz`.
Windows assets should end with `.zip`.

Examples that will match:

- `avtkit_v1.4.0_darwin_arm64.tar.gz`
- `avtkit_v1.4.0_linux_amd64.tar.gz`
- `avtkit-v1.4.0-windows-amd64.zip`

For the Unix install script to work, release archives should extract to a file named `avtkit`.

## Deployment

1. Install dependencies:

```bash
cd worker/release-router
npm install
```

2. Optional: add a GitHub token to raise API rate limits.

```bash
npx wrangler secret put GITHUB_TOKEN
```

3. Deploy:

```bash
npm run deploy
```

4. Attach a route or custom domain in the Cloudflare dashboard, or add routes to `wrangler.toml` once the final domain is known.

## Local verification

```bash
cd worker/release-router
npm test
```

The tests cover:

- bootstrap script generation
- latest-release JSON response
- platform-to-asset matching
- redirect behavior for `/releases/latest/download/:os/:arch`

## Configuration

`wrangler.toml` exposes these Worker variables:

- `GITHUB_OWNER`
- `GITHUB_REPO`
- `CLI_NAME`
- `DEFAULT_INSTALL_DIR`

Optional secret:

- `GITHUB_TOKEN`

## Notes for the later release workflow

This PR only adds the Worker MVP. It does not add the GitHub Release build pipeline yet.

When the release workflow lands, it should publish multi-platform archives whose names follow the contract above. Once that exists, the Worker endpoints become production-ready without changing the public curl URLs.
