import { renderBootstrapScript } from "./bootstrap-script.mjs";

const DEFAULT_CONFIG = {
  cliName: "avtkit",
  defaultInstallDir: "/usr/local/bin",
  githubApiBaseUrl: "https://api.github.com",
  githubOwner: "spatialwalk",
  githubRepo: "open-platform-cli",
  githubToken: "",
};

const CACHE_CONTROL = "public, max-age=300";
const LATEST_RELEASE_CACHE_TTL_SECONDS = 600;
const LATEST_RELEASE_CACHE_CONTROL = `public, max-age=${LATEST_RELEASE_CACHE_TTL_SECONDS}`;
const INSTALL_ROUTES = new Set(["/install", "/install.sh"]);
const UPGRADE_ROUTES = new Set(["/upgrade", "/upgrade.sh"]);

export function createReleaseRouter(overrides = {}) {
  const staticConfig = { ...DEFAULT_CONFIG, ...overrides };
  return {
    async fetch(request, env = {}) {
      try {
        return await handleRequest(request, resolveConfig(env, staticConfig));
      } catch (error) {
        if (error instanceof Response) {
          return error;
        }

        return jsonResponse(
          {
            error: "internal_error",
            message: error instanceof Error ? error.message : "unexpected worker failure",
          },
          500,
          request.method === "HEAD",
        );
      }
    },
  };
}

export async function handleRequest(request, config) {
  const url = new URL(request.url);

  if (request.method !== "GET" && request.method !== "HEAD") {
    return jsonResponse(
      { error: "method_not_allowed", message: "only GET and HEAD are supported" },
      405,
      request.method === "HEAD",
    );
  }

  if (url.pathname === "/healthz") {
    return jsonResponse(
      {
        ok: true,
        repo: `${config.githubOwner}/${config.githubRepo}`,
      },
      200,
      request.method === "HEAD",
    );
  }

  if (INSTALL_ROUTES.has(url.pathname) || UPGRADE_ROUTES.has(url.pathname)) {
    const mode = INSTALL_ROUTES.has(url.pathname) ? "install" : "upgrade";
    const body = renderBootstrapScript({
      baseUrl: url.origin,
      cliName: config.cliName,
      defaultInstallDir: config.defaultInstallDir,
      mode,
    });
    return textResponse(body, 200, request.method === "HEAD", {
      "content-type": "text/x-shellscript; charset=utf-8",
    });
  }

  if (url.pathname === "/latest") {
    const release = await fetchLatestRelease(config);
    return jsonResponse(
      buildLatestResponse(url, config, release),
      200,
      request.method === "HEAD",
    );
  }

  const downloadMatch = url.pathname.match(
    /^\/releases\/latest\/download\/(?<os>[a-z0-9._-]+)\/(?<arch>[a-z0-9._-]+)$/,
  );
  if (downloadMatch?.groups) {
    const platform = normalizePlatform(downloadMatch.groups.os, downloadMatch.groups.arch);
    const release = await fetchLatestRelease(config);
    const asset = selectAssetForPlatform(release.assets ?? [], config.cliName, platform);
    if (!asset) {
      return jsonResponse(
        {
          error: "asset_not_found",
          message: `no release asset matched ${platform.os}/${platform.arch}`,
          platform,
          release: release.tag_name ?? "",
        },
        404,
        request.method === "HEAD",
      );
    }

    const headers = new Headers({
      location: asset.browser_download_url,
      "cache-control": CACHE_CONTROL,
      "x-release-tag": release.tag_name ?? "",
      "x-release-asset": asset.name,
    });
    return new Response(null, { status: 302, headers });
  }

  return jsonResponse(
    {
      error: "not_found",
      message: "supported paths: /install.sh, /upgrade.sh, /latest, /releases/latest/download/:os/:arch, /healthz",
    },
    404,
    request.method === "HEAD",
  );
}

export function normalizePlatform(osInput, archInput) {
  const os = normalizeOs(osInput);
  const arch = normalizeArch(archInput);
  return { os, arch };
}

export function selectAssetForPlatform(assets, cliName, platform) {
  const candidates = assets
    .filter((asset) => assetMatchesPlatform(asset.name, cliName, platform))
    .sort((left, right) => scoreAsset(right.name, platform) - scoreAsset(left.name, platform));

  return candidates[0] ?? null;
}

function resolveConfig(env, staticConfig) {
  return {
    cliName: env.CLI_NAME ?? staticConfig.cliName,
    defaultInstallDir: env.DEFAULT_INSTALL_DIR ?? staticConfig.defaultInstallDir,
    githubApiBaseUrl: env.GITHUB_API_BASE_URL ?? staticConfig.githubApiBaseUrl,
    githubOwner: env.GITHUB_OWNER ?? staticConfig.githubOwner,
    githubRepo: env.GITHUB_REPO ?? staticConfig.githubRepo,
    githubToken: env.GITHUB_TOKEN ?? staticConfig.githubToken,
  };
}

async function fetchLatestRelease(config) {
  const cache = globalThis.caches?.default;
  const cacheKey = buildLatestReleaseCacheKey(config);

  if (cache) {
    const cachedResponse = await cache.match(cacheKey);
    if (cachedResponse) {
      return cachedResponse.json();
    }
  }

  const releaseUrl = `${config.githubApiBaseUrl.replace(/\/+$/, "")}/repos/${config.githubOwner}/${config.githubRepo}/releases/latest`;
  const headers = new Headers({
    accept: "application/vnd.github+json",
    "user-agent": `${config.cliName}-release-router`,
  });
  if (config.githubToken) {
    headers.set("authorization", `Bearer ${config.githubToken}`);
  }

  const response = await fetch(releaseUrl, { headers });
  if (!response.ok) {
    let message = `GitHub latest release request failed with status ${response.status}`;
    try {
      const payload = await response.json();
      if (typeof payload?.message === "string" && payload.message !== "") {
        message = payload.message;
      }
    } catch {
      // Keep the fallback message when GitHub did not return JSON.
    }

    throw new Response(
      JSON.stringify({
        error: "release_lookup_failed",
        message,
      }),
      {
        status: 502,
        headers: {
          "cache-control": "no-store",
          "content-type": "application/json; charset=utf-8",
        },
      },
    );
  }

  const release = await response.json();

  if (cache) {
    await cache.put(
      cacheKey,
      new Response(JSON.stringify(release), {
        headers: {
          "cache-control": LATEST_RELEASE_CACHE_CONTROL,
          "content-type": "application/json; charset=utf-8",
        },
      }),
    );
  }

  return release;
}

function buildLatestReleaseCacheKey(config) {
  const cacheUrl = new URL("https://release-router-cache.internal/github/latest");
  cacheUrl.searchParams.set("api_base", config.githubApiBaseUrl.replace(/\/+$/, ""));
  cacheUrl.searchParams.set("owner", config.githubOwner);
  cacheUrl.searchParams.set("repo", config.githubRepo);
  return new Request(cacheUrl.toString(), {
    method: "GET",
    headers: {
      accept: "application/json",
    },
  });
}

function buildLatestResponse(url, config, release) {
  const baseOrigin = url.origin;
  return {
    repo: `${config.githubOwner}/${config.githubRepo}`,
    cli_name: config.cliName,
    tag_name: release.tag_name ?? "",
    published_at: release.published_at ?? "",
    release_url: release.html_url ?? "",
    install_url: `${baseOrigin}/install.sh`,
    upgrade_url: `${baseOrigin}/upgrade.sh`,
    download_url_template: `${baseOrigin}/releases/latest/download/{os}/{arch}`,
    assets:
      Array.isArray(release.assets) ?
        release.assets.map((asset) => ({
          name: asset.name,
          size: asset.size,
          download_url: asset.browser_download_url,
        }))
      : [],
  };
}

function assetMatchesPlatform(name, cliName, platform) {
  const normalized = name.toLowerCase();
  if (!normalized.includes(cliName.toLowerCase())) {
    return false;
  }

  if (!matchesToken(normalized, platform.os)) {
    return false;
  }

  const archAliases = ARCH_ALIASES[platform.arch] ?? [platform.arch];
  if (!archAliases.some((alias) => matchesToken(normalized, alias))) {
    return false;
  }

  return hasSupportedArchive(normalized, platform.os);
}

function scoreAsset(name, platform) {
  const normalized = name.toLowerCase();
  let score = 0;
  if (normalized.endsWith(".tar.gz")) {
    score += platform.os === "windows" ? 0 : 20;
  }
  if (normalized.endsWith(".zip")) {
    score += platform.os === "windows" ? 20 : 5;
  }
  if (normalized.includes("checksums")) {
    score -= 100;
  }
  if (normalized.includes("sha256")) {
    score -= 100;
  }
  return score;
}

function hasSupportedArchive(name, os) {
  if (os === "windows") {
    return name.endsWith(".zip");
  }
  return name.endsWith(".tar.gz") || name.endsWith(".tgz");
}

function matchesToken(value, token) {
  const escaped = escapeRegExp(token.toLowerCase());
  const pattern = new RegExp(`(?:^|[^a-z0-9])${escaped}(?:$|[^a-z0-9])`);
  return pattern.test(value);
}

function normalizeOs(value) {
  const normalized = value.toLowerCase();
  switch (normalized) {
    case "darwin":
    case "macos":
      return "darwin";
    case "linux":
      return "linux";
    case "windows":
    case "win32":
      return "windows";
    default:
      return normalized;
  }
}

function normalizeArch(value) {
  const normalized = value.toLowerCase();
  switch (normalized) {
    case "x86_64":
    case "x64":
    case "amd64":
      return "amd64";
    case "arm64":
    case "aarch64":
      return "arm64";
    default:
      return normalized;
  }
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function jsonResponse(payload, status, headOnly) {
  return createResponse(JSON.stringify(payload, null, 2), status, headOnly, {
    "content-type": "application/json; charset=utf-8",
  });
}

function textResponse(body, status, headOnly, headers = {}) {
  return createResponse(body, status, headOnly, headers);
}

function createResponse(body, status, headOnly, headers = {}) {
  const responseHeaders = new Headers({
    "cache-control": CACHE_CONTROL,
    ...headers,
  });
  return new Response(headOnly ? null : body, {
    status,
    headers: responseHeaders,
  });
}

const ARCH_ALIASES = {
  amd64: ["amd64", "x86_64", "x64"],
  arm64: ["arm64", "aarch64"],
};
