// Validation only: no release, ref, repository or asset writes.
const fs = require('node:fs');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const path = require('node:path');
const commit = 'acac2f0eae0d98e42f442c7f4e6a36ccf9bc3665';
const expected = {
  'crier-darwin-amd64': 'acf8662685a49b097582b8aea144239898d8bb4fdd52fede9b369f9fd2f80530',
  'crier-darwin-arm64': '7a0cbf9d60c94973659a8d8f2b51ed72841439228cfde8839f118fb6693ea105',
  'crier-linux-amd64': 'abbf9ce00d7302dc9c684ade29fc0083e43dc63407709cde0b0fb50efa162dc1',
  'crier-linux-arm64': '898aefa0ebb2dbc5c812a0cff33366fd8907708cc51b5bf7132bfc82a0376ddd',
  'crier-windows-amd64.exe': 'c20a999f3a06a7af2803869ade55deb24e7f8fdf33d48d14f87c62cea77b638c',
  'crier-windows-arm64.exe': '514e2c41eaa291bc35e02d2c0d5ecc1a05cf5cc7ae2e72f85f2e3cffba07a037',
  'crier-tiny-linux-amd64': '131e99452edadb24d7fb9dc601e029310fad645338415d57215cc3321b034d5c',
  'crier-tiny-linux-arm64': 'a368282f1b8757438cd64861ed5ea0a1773219d41a7a509920d85df0dfa99e0f',
  'crier-v1.1.1-acceptance.md': '2734698ebca8c4bb836195a1f70bd8fb1921fe46c5ba66272d9555d9d1b5cf1c',
  SHA256SUMS: 'd72f206e0b759e90aa8140ed7ee2b24c0cfd78523016f54191881a714ec40d31',
};
const sha = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
module.exports = async ({github, core}) => {
  const repo = {owner: 'yohimik', repo: 'crier'};
  const {data: tag} = await github.rest.git.getRef({...repo, ref: 'tags/v1.1.1'});
  assert.equal(tag.object.sha, commit);
  assert.equal(tag.object.type, 'commit');
  const {data: release} = await github.rest.repos.getReleaseByTag({...repo, tag: 'v1.1.1'});
  assert.equal(release.id, 383677676);
  assert.equal(release.draft, false);
  assert.equal(release.prerelease, false);
  assert.equal(release.assets.length, 10);
  for (const asset of release.assets) {
    assert.equal(asset.digest, `sha256:${expected[asset.name]}`, asset.name);
    assert.equal(asset.state, 'uploaded');
  }
  const os = process.env.TARGET_OS, arch = process.env.TARGET_ARCH;
  assert.equal(process.platform, {linux:'linux',darwin:'darwin',windows:'win32'}[os]);
  assert.equal(process.arch, arch === 'amd64' ? 'x64' : 'arm64');
  const name = `crier-${os}-${arch}${os === 'windows' ? '.exe' : ''}`;
  const names = [name, 'SHA256SUMS', 'crier-v1.1.1-acceptance.md'];
  if (os === 'linux') names.push(`crier-tiny-linux-${arch}`);
  fs.mkdirSync('evidence', {recursive:true});
  for (const name of names) {
    const asset = release.assets.find(a => a.name === name);
    const response = await fetch(asset.browser_download_url);
    assert(response.ok, `${name}: HTTP ${response.status}`);
    const bytes = Buffer.from(await response.arrayBuffer());
    assert.equal(sha(bytes), expected[name], name);
    assert.equal(bytes.length, asset.size);
    fs.writeFileSync(path.join('evidence', name), bytes, {mode:0o755});
    core.info(`Verified ${name}: ${bytes.length} bytes ${expected[name]}`);
  }
  fs.writeFileSync('evidence/release.json', JSON.stringify(release, null, 2));
  core.setOutput('binary', path.resolve('evidence', name));
};
module.exports.verifyInstalled = (file, os, arch) => {
  const name = `crier-${os}-${arch}${os === 'windows' ? '.exe' : ''}`;
  assert.equal(sha(fs.readFileSync(file)), expected[name], `installed ${file}`);
  console.log(`Verified installed ${file} matches published ${name}`);
};
module.exports.verifySmoke = (file, os) => {
  const events = fs.readFileSync(file, 'utf8').trim().split('\n').map(JSON.parse);
  const tests = ['TestSmokeHostileChangelog', 'TestSmokePagedPostsLandEverywhere',
    'TestSmokeStoriesRecoverFromNotReady', 'TestSmokeRenderProducesAPNG',
    'TestSmokeFlagsOverrideTheEnvironment', 'TestSmokePublishToEveryPlatform',
    'TestSmokeVersionFlag'];
  assert(!events.some(e => e.Action === 'fail'), 'smoke failure');
  assert(events.some(e => e.Action === 'pass' && !e.Test), 'package did not pass');
  const skipped = events.filter(e => e.Action === 'skip').map(e => e.Test);
  assert.deepEqual(skipped, os === 'windows' ? ['TestSmokeHostileChangelog'] : []);
  if (os === 'windows') {
    assert(events.some(e => e.Test === 'TestSmokeHostileChangelog' &&
      e.Output?.includes('the announce scripts are sh')), 'unexpected skip reason');
  }
  const passed = events.filter(e => e.Action === 'pass' && e.Test).map(e => e.Test).sort();
  assert.deepEqual(passed, tests.filter(t => !skipped.includes(t)).sort());
  console.log(`Smoke: ${passed.length} passed, 0 failed, ${skipped.length} documented platform skips: ${skipped.join(', ')}`);
};
