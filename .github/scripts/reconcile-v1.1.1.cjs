// One-time, idempotent metadata repair. Never creates a release or writes assets
// or the exact version tag. Ordinary future releases must run release.yml.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const repo = {owner: 'yohimik', repo: 'crier'};
const accepted = 'acac2f0eae0d98e42f442c7f4e6a36ccf9bc3665';
const previous = '00db2615ea165669870a63da77d4f6f23bf1a5dc';
const evidenceHead = '3fcf16c828c2d5b387ddf48ee7948416d4adba56';
const fingerprint = release => release.assets.map(a => ({
  id:a.id, name:a.name, size:a.size, digest:a.digest, state:a.state,
})).sort((a,b) => a.name.localeCompare(b.name));

module.exports = async ({github, core, context,
  verifyArtifacts = require('./validate-v1.1.1.cjs')}) => {
  assert.equal(context.eventName, 'workflow_dispatch');
  assert.equal(context.ref, 'refs/heads/main');
  assert.equal(`${context.repo.owner}/${context.repo.repo}`, 'yohimik/crier');
  const {data: main} = await github.rest.git.getRef({...repo, ref:'heads/main'});
  assert.equal(main.object.sha, context.sha, 'main moved; dispatch at its current commit');
  const {data: ancestry} = await github.rest.repos.compareCommitsWithBasehead({
    ...repo, basehead:`${accepted}...${context.sha}`,
  });
  assert(['ahead', 'identical'].includes(ancestry.status), 'released source must be an ancestor of main');
  const {data: runs} = await github.rest.actions.listWorkflowRuns({
    ...repo, workflow_id:'tests.yml', head_sha:context.sha, event:'push', status:'completed', per_page:100,
  });
  assert(runs.workflow_runs.some(r => r.head_sha === context.sha &&
    r.head_branch === 'main' && r.event === 'push' && r.conclusion === 'success'),
  'current main must pass its push CI before metadata reconciliation');
  const {data: evidence} = await github.rest.actions.getWorkflowRun({...repo, run_id:34061068016});
  assert.equal(evidence.head_sha, evidenceHead);
  assert.equal(evidence.path, '.github/workflows/validate-v1.1.1.yml');
  assert.equal(evidence.conclusion, 'success');
  const {data: latest} = await github.rest.repos.getLatestRelease(repo);
  assert.equal(latest.tag_name, 'v1.1.1', 'never move the stable alias backwards after a newer release');
  await verifyArtifacts({github, core});
  const getRelease = async () => (await github.rest.repos.getReleaseByTag({...repo, tag:'v1.1.1'})).data;
  const before = await getRelease();
  assert.equal(before.id, 383677676);
  const getAlias = async () => {
    const {data: ref} = await github.rest.git.getRef({...repo, ref:'tags/v1'});
    let object = ref.object;
    for (let depth = 0; object.type === 'tag' && depth < 5; depth++) {
      object = (await github.rest.git.getTag({...repo, tag_sha:object.sha})).data.object;
    }
    assert.equal(object.type, 'commit');
    assert([previous, accepted].includes(object.sha), 'unexpected v1 target; do not overwrite it');
    return {refSha:ref.object.sha, commit:object.sha};
  };
  const alias = await getAlias();
  const body = fs.readFileSync('docs/releases/v1.1.1.md', 'utf8');
  for (const heading of ['### Fixes', '### Authors', '## Install']) assert(body.includes(heading));

  // Recheck immediately before writes. Shared concurrency with release.yml
  // prevents the ordinary release workflow racing this reconciliation.
  assert.equal((await github.rest.git.getRef({...repo, ref:'heads/main'})).data.object.sha, context.sha);
  assert.deepEqual(await getAlias(), alias);
  assert.equal((await github.rest.repos.getLatestRelease(repo)).data.tag_name, 'v1.1.1');
  if (before.name !== 'v1.1.1' || before.body !== body) {
    await github.rest.repos.updateRelease({...repo, release_id:before.id, name:'v1.1.1', body});
  }
  if (alias.commit !== accepted) {
    const {data: tag} = await github.rest.git.createTag({
      ...repo, tag:'v1', message:'release v1.1.1', object:accepted, type:'commit',
    });
    assert.deepEqual(await getAlias(), alias);
    // Only the declared moving major alias may be force-updated.
    await github.rest.git.updateRef({...repo, ref:'tags/v1', sha:tag.sha, force:true});
  }
  const after = await getRelease();
  assert.equal(after.name, 'v1.1.1');
  assert.equal(after.body, body);
  assert.deepEqual(fingerprint(after), fingerprint(before), 'release assets changed');
  assert.equal(after.draft, false);
  assert.equal(after.prerelease, false);
  assert.equal((await github.rest.git.getRef({...repo, ref:'tags/v1.1.1'})).data.object.sha, accepted);
  assert.equal((await getAlias()).commit, accepted);
  core.info('Reconciled existing v1.1.1 title/body and moving v1 alias; exact tag and all ten assets unchanged.');
};
