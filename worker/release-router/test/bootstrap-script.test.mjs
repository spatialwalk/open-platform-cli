import test from "node:test";
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { chmod, mkdtemp, mkdir, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";

import { renderBootstrapScript } from "../src/bootstrap-script.mjs";

const execFileAsync = promisify(execFile);

test("install script advertises the fresh-install guard", () => {
  const script = renderBootstrapScript({
    baseUrl: "https://downloads.example.com",
    cliName: "avtkit",
    mode: "install",
  });

  assert.match(script, /found an existing \$CLI_NAME binary at \$target_path/);
  assert.match(script, /install\.sh only supports fresh installs and will not overwrite an existing binary/);
  assert.match(script, /curl -fsSL \$BASE_URL\/upgrade\.sh \| sh/);
});

test("fresh install writes the avtkit binary", async () => {
  const sandbox = await createBootstrapSandbox();
  const result = await runBootstrapScript({
    sandbox,
    mode: "install",
  });

  assert.equal(result.code, 0, result.stderr);
  assert.match(result.stderr, /install avtkit for/);
  assert.match(result.stderr, /avtkit 9\.9\.9/);

  const installedBinary = await readFile(path.join(sandbox.installDir, "avtkit"), "utf8");
  assert.match(installedBinary, /avtkit 9\.9\.9/);
  assert.equal(await fileExists(sandbox.curlMarker), true);
});

test("install aborts before download when avtkit already exists", async () => {
  const sandbox = await createBootstrapSandbox();
  const targetPath = path.join(sandbox.installDir, "avtkit");
  const existingBinary = createFixtureBinary("1.0.0");
  await writeFile(targetPath, existingBinary);
  await chmod(targetPath, 0o755);

  const result = await runBootstrapScript({
    sandbox,
    mode: "install",
  });

  assert.equal(result.code, 1);
  assert.match(result.stderr, new RegExp(`found an existing avtkit binary at ${escapeRegExp(targetPath)}`));
  assert.match(result.stderr, /install\.sh only supports fresh installs and will not overwrite an existing binary/);
  assert.match(result.stderr, /run the upgrade script instead: curl -fsSL https:\/\/downloads\.example\.com\/upgrade\.sh \| sh/);
  assert.equal(await fileExists(sandbox.curlMarker), false);

  const installedBinary = await readFile(targetPath, "utf8");
  assert.equal(installedBinary, existingBinary);
});

test("upgrade replaces an existing avtkit binary", async () => {
  const sandbox = await createBootstrapSandbox();
  const targetPath = path.join(sandbox.installDir, "avtkit");
  await writeFile(targetPath, createFixtureBinary("1.0.0"));
  await chmod(targetPath, 0o755);

  const result = await runBootstrapScript({
    sandbox,
    mode: "upgrade",
  });

  assert.equal(result.code, 0, result.stderr);
  assert.match(result.stderr, /upgrade avtkit for/);
  assert.match(result.stderr, /avtkit 9\.9\.9/);
  assert.equal(await fileExists(sandbox.curlMarker), true);

  const installedBinary = await readFile(targetPath, "utf8");
  assert.match(installedBinary, /avtkit 9\.9\.9/);
});

async function createBootstrapSandbox() {
  const rootDir = await mkdtemp(path.join(tmpdir(), "avtkit-bootstrap-test-"));
  const fakeBinDir = path.join(rootDir, "fake-bin");
  const payloadDir = path.join(rootDir, "payload");
  const installDir = path.join(rootDir, "install");
  const archivePath = path.join(rootDir, "avtkit.tar.gz");
  const curlMarker = path.join(rootDir, "curl-called");

  await mkdir(fakeBinDir, { recursive: true });
  await mkdir(payloadDir, { recursive: true });
  await mkdir(installDir, { recursive: true });

  const payloadBinaryPath = path.join(payloadDir, "avtkit");
  await writeFile(payloadBinaryPath, createFixtureBinary("9.9.9"));
  await chmod(payloadBinaryPath, 0o755);

  await execFileAsync("tar", ["-czf", archivePath, "-C", payloadDir, "avtkit"]);

  const fakeCurlPath = path.join(fakeBinDir, "curl");
  await writeFile(
    fakeCurlPath,
    `#!/bin/sh
set -eu

output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

[ -n "$output" ] || exit 2
cp "$FAKE_ARCHIVE" "$output"

if [ -n "\${CURL_MARKER:-}" ]; then
  : > "$CURL_MARKER"
fi
`,
  );
  await chmod(fakeCurlPath, 0o755);

  return {
    archivePath,
    curlMarker,
    fakeBinDir,
    installDir,
    rootDir,
  };
}

async function runBootstrapScript({ sandbox, mode }) {
  const scriptPath = path.join(sandbox.rootDir, `${mode}.sh`);
  await writeFile(
    scriptPath,
    renderBootstrapScript({
      baseUrl: "https://downloads.example.com",
      cliName: "avtkit",
      defaultInstallDir: sandbox.installDir,
      mode,
    }),
  );
  await chmod(scriptPath, 0o755);

  const env = {
    ...process.env,
    AVTKIT_INSTALL_DIR: sandbox.installDir,
    CURL_MARKER: sandbox.curlMarker,
    FAKE_ARCHIVE: sandbox.archivePath,
    HOME: sandbox.rootDir,
    PATH: `${sandbox.fakeBinDir}:${process.env.PATH ?? ""}`,
  };

  try {
    const { stdout, stderr } = await execFileAsync("/bin/sh", [scriptPath], {
      env,
    });
    return { code: 0, stdout, stderr };
  } catch (error) {
    return {
      code: error.code ?? 1,
      stdout: error.stdout ?? "",
      stderr: error.stderr ?? "",
    };
  }
}

function createFixtureBinary(version) {
  return `#!/bin/sh
set -eu

if [ "\${1:-}" = "version" ]; then
  printf 'avtkit ${version}\\n'
  exit 0
fi

printf 'avtkit ${version}\\n'
`;
}

async function fileExists(filePath) {
  try {
    await readFile(filePath);
    return true;
  } catch {
    return false;
  }
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
