import test from "node:test";
import assert from "node:assert/strict";

import {
  createReleaseRouter,
  normalizePlatform,
  selectAssetForPlatform,
} from "../src/release-router.mjs";

const originalFetch = globalThis.fetch;

const sampleRelease = {
  tag_name: "v1.4.0",
  published_at: "2026-03-29T12:00:00Z",
  html_url: "https://github.com/spatialwalk/open-platform-cli/releases/tag/v1.4.0",
  assets: [
    {
      name: "avtkit_v1.4.0_darwin_arm64.tar.gz",
      size: 123,
      browser_download_url: "https://github.com/spatialwalk/open-platform-cli/releases/download/v1.4.0/avtkit_v1.4.0_darwin_arm64.tar.gz",
    },
    {
      name: "avtkit_v1.4.0_linux_amd64.tar.gz",
      size: 456,
      browser_download_url: "https://github.com/spatialwalk/open-platform-cli/releases/download/v1.4.0/avtkit_v1.4.0_linux_amd64.tar.gz",
    },
    {
      name: "avtkit_v1.4.0_windows_amd64.zip",
      size: 789,
      browser_download_url: "https://github.com/spatialwalk/open-platform-cli/releases/download/v1.4.0/avtkit_v1.4.0_windows_amd64.zip",
    },
    {
      name: "avtkit_v1.4.0_checksums.txt",
      size: 80,
      browser_download_url: "https://github.com/spatialwalk/open-platform-cli/releases/download/v1.4.0/avtkit_v1.4.0_checksums.txt",
    },
  ],
};

test("normalizePlatform collapses common aliases", () => {
  assert.deepEqual(normalizePlatform("macos", "x86_64"), {
    os: "darwin",
    arch: "amd64",
  });
  assert.deepEqual(normalizePlatform("linux", "aarch64"), {
    os: "linux",
    arch: "arm64",
  });
});

test("selectAssetForPlatform chooses the matching release archive", () => {
  const asset = selectAssetForPlatform(sampleRelease.assets, "avtkit", {
    os: "darwin",
    arch: "arm64",
  });
  assert.ok(asset);
  assert.equal(asset.name, "avtkit_v1.4.0_darwin_arm64.tar.gz");
});

test("worker serves install script with stable download endpoint", async (t) => {
  stubGitHubReleaseLookup(t, sampleRelease);
  const worker = createReleaseRouter();
  const response = await worker.fetch(new Request("https://downloads.example.com/install.sh"));

  assert.equal(response.status, 200);
  assert.equal(response.headers.get("content-type"), "text/x-shellscript; charset=utf-8");

  const body = await response.text();
  assert.match(body, /MODE='install'/);
  assert.match(body, /DOWNLOAD_URL="\$BASE_URL\/releases\/latest\/download"/);
  assert.match(body, /curl -fsSL "\$DOWNLOAD_URL\/\$os\/\$arch" -o "\$archive_path"/);
});

test("worker exposes latest release metadata", async (t) => {
  stubGitHubReleaseLookup(t, sampleRelease);
  const worker = createReleaseRouter();
  const response = await worker.fetch(new Request("https://downloads.example.com/latest"));

  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.equal(payload.tag_name, "v1.4.0");
  assert.equal(payload.install_url, "https://downloads.example.com/install.sh");
  assert.equal(payload.assets.length, 4);
});

test("worker redirects download route to the matched release asset", async (t) => {
  stubGitHubReleaseLookup(t, sampleRelease);
  const worker = createReleaseRouter();
  const response = await worker.fetch(
    new Request("https://downloads.example.com/releases/latest/download/linux/amd64"),
  );

  assert.equal(response.status, 302);
  assert.equal(
    response.headers.get("location"),
    "https://github.com/spatialwalk/open-platform-cli/releases/download/v1.4.0/avtkit_v1.4.0_linux_amd64.tar.gz",
  );
  assert.equal(response.headers.get("x-release-tag"), "v1.4.0");
});

test("worker returns 404 when the platform does not exist in the latest release", async (t) => {
  stubGitHubReleaseLookup(t, sampleRelease);
  const worker = createReleaseRouter();
  const response = await worker.fetch(
    new Request("https://downloads.example.com/releases/latest/download/linux/arm64"),
  );

  assert.equal(response.status, 404);
  const payload = await response.json();
  assert.equal(payload.error, "asset_not_found");
});

test("worker rejects unsupported methods", async () => {
  const worker = createReleaseRouter();
  const response = await worker.fetch(
    new Request("https://downloads.example.com/install.sh", { method: "POST" }),
  );

  assert.equal(response.status, 405);
});

function stubGitHubReleaseLookup(t, release) {
  t.after(() => {
    globalThis.fetch = originalFetch;
  });
  globalThis.fetch = async (input) => {
    const url = typeof input === "string" ? input : input.url;
    assert.match(url, /\/releases\/latest$/);
    return new Response(JSON.stringify(release), {
      status: 200,
      headers: {
        "content-type": "application/json; charset=utf-8",
      },
    });
  };
}
