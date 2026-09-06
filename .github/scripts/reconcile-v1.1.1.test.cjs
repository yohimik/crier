const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const reconcile = require('./reconcile-v1.1.1.cjs');
const accepted = 'acac2f0eae0d98e42f442c7f4e6a36ccf9bc3665';
const previous = '00db2615ea165669870a63da77d4f6f23bf1a5dc';
const body = fs.readFileSync('docs/releases/v1.1.1.md', 'utf8');

function fixture() {
  const state = {
    main:'main-sha', ancestry:'ahead', latest:'v1.1.1', alias:'old-tag', aliasCommit:previous,
    conclusion:'success', evidence:'success', verified:0, writes:[],
    release:{id:383677676, name:'Crier v1.1.1', body:'old notes', draft:false, prerelease:false,
      assets:[{id:1, name:'test-asset', size:10, digest:'sha256:unchanged', state:'uploaded'}]},
  };
  const reply = data => ({data:structuredClone(data)});
  const github = {rest:{
    git:{
      getRef:async ({ref}) => {
        assert(['heads/main', 'tags/v1', 'tags/v1.1.1'].includes(ref));
        return reply({object:ref === 'heads/main' ? {sha:state.main, type:'commit'} :
          ref === 'tags/v1.1.1' ? {sha:accepted, type:'commit'} : {sha:state.alias, type:'tag'}});
      },
      getTag:async () => reply({object:{sha:state.aliasCommit, type:'commit'}}),
      createTag:async args => {
        assert.equal(args.tag, 'v1'); assert.equal(args.object, accepted);
        state.writes.push('create annotated v1'); return reply({sha:'new-tag'});
      },
      updateRef:async args => {
        assert.equal(args.ref, 'tags/v1'); assert.equal(args.sha, 'new-tag');
        assert.equal(args.force, true);
        state.writes.push('update v1'); state.alias = 'new-tag'; state.aliasCommit = accepted;
        return reply({});
      },
    },
    repos:{
      compareCommitsWithBasehead:async () => reply({status:state.ancestry}),
      getLatestRelease:async () => reply({tag_name:state.latest}),
      getReleaseByTag:async () => reply(state.release),
      updateRelease:async args => {
        assert.deepEqual(Object.keys(args).sort(), ['body','name','owner','release_id','repo']);
        assert.equal(args.release_id, 383677676);
        state.writes.push('update release metadata');
        state.release.name = args.name; state.release.body = args.body;
        if (state.tamper) state.release.assets[0].digest = 'sha256:changed';
        return reply(state.release);
      },
    },
    actions:{
      listWorkflowRuns:async args => {
        assert.equal(args.workflow_id, 'tests.yml'); assert.equal(args.event, 'push');
        return reply({workflow_runs:[{head_sha:state.main, head_branch:'main', event:'push', conclusion:state.conclusion}]});
      },
      getWorkflowRun:async ({run_id}) => {
        assert.equal(run_id, 34061068016);
        return reply({head_sha:'3fcf16c828c2d5b387ddf48ee7948416d4adba56',
          path:'.github/workflows/validate-v1.1.1.yml', conclusion:state.evidence});
      },
    },
  }};
  const args = {github, core:{info:() => {}},
    context:{eventName:'workflow_dispatch', ref:'refs/heads/main', sha:'main-sha', repo:{owner:'yohimik',repo:'crier'}},
    verifyArtifacts:async () => { state.verified++; assert(!state.badArtifacts, 'bad immutable artifacts'); },
  };
  return {state, args};
}

test('repairs only existing metadata and moving alias, and is idempotent', async () => {
  const {state,args} = fixture();
  await reconcile(args);
  assert.deepEqual(state.writes, ['update release metadata', 'create annotated v1', 'update v1']);
  assert.equal(state.release.body, body);
  assert.equal(state.aliasCommit, accepted);
  state.writes = [];
  await reconcile(args);
  assert.deepEqual(state.writes, []);
  assert.equal(state.verified, 2);
});

for (const [name, mutate] of Object.entries({
  'non-dispatch event':({args}) => { args.context.eventName = 'push'; },
  'non-main branch':({args}) => { args.context.ref = 'refs/heads/other'; },
  'wrong repository':({args}) => { args.context.repo.owner = 'someone-else'; },
  'main moved':({state}) => { state.main = 'new-main'; },
  'release not in main ancestry':({state}) => { state.ancestry = 'diverged'; },
  'main CI failing':({state}) => { state.conclusion = 'failure'; },
  'acceptance CI failing':({state}) => { state.evidence = 'failure'; },
  'newer release exists':({state}) => { state.latest = 'v1.2.0'; },
  'unexpected alias':({state}) => { state.aliasCommit = 'unexpected'; },
  'artifact verification fails':({state}) => { state.badArtifacts = true; },
  'unexpected release id':({state}) => { state.release.id = 99; },
})) {
  test(`refuses ${name} before any write`, async () => {
    const f = fixture(); mutate(f);
    await assert.rejects(reconcile(f.args));
    assert.deepEqual(f.state.writes, []);
  });
}

test('detects assets changing during metadata reconciliation', async () => {
  const {state,args} = fixture(); state.tamper = true;
  await assert.rejects(reconcile(args), /release assets changed/);
});
