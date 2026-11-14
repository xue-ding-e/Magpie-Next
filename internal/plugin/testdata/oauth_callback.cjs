// Exercise production callback/save with synthetic credentials and storage.
const assert = require('node:assert/strict')
const fs = require('node:fs')
const vm = require('node:vm')
const source = fs.readFileSync(0, 'utf8')
function section(from, to) {
  const start = source.indexOf(from), end = source.indexOf(to, start)
  assert.ok(start >= 0 && end > start)
  return source.slice(start, end)
}
const code = section('async function save(', '// signInKey') + section('function failed(', 'async function apiKey(')
async function run(result, method = 'auto') {
  const writes = [], scopes = []
  const sessions = new Map([['fixture', {provider: 'fake', key: 'fake#1', proxy: 'direct', a: {
    method, callback: async value => { assert.equal(value, method === 'code' ? 'synthetic-code' : undefined); return result }
  }}]])
  const context = vm.createContext({sessions,
    via: {run: async (proxy, fn) => { assert.equal(proxy, 'direct'); return fn() }},
    inScope: (provider, key, fn) => { scopes.push([provider,key]); return fn() },
    signInSignal: {run: (_signal, fn) => fn()},
    setAuth: async (key, value) => { await Promise.resolve(); writes.push({key,value}) },
    settle: async (_, key) => key, freshKey: provider => provider + '#new'
  })
  vm.runInContext(code + ';globalThis.invoke=callback', context)
  const answer = await context.invoke({session:'fixture',code:'synthetic-code'})
  assert.equal(sessions.size, 0)
  await assert.rejects(() => context.invoke({session:'fixture'}), /no such sign-in/)
  assert.deepEqual(scopes,[['fake','fake#1']])
  return JSON.parse(JSON.stringify({answer,writes}))
}
;(async () => {
  for (const method of ['auto','code']) {
    const r = await run({type:'success',access:'synthetic-access',expires:4102444800000,accountId:'fixture'}, method)
    assert.equal(r.answer.ok,true)
    assert.deepEqual(r.writes,[{key:'fake#1',value:{type:'oauth',refresh:'',access:'synthetic-access',expires:4102444800000,accountId:'fixture'}}])
  }
  for (const result of [{type:'success'}, {type:'success',access:''}, {type:'success',access:42}]) {
    const r = await run(result)
    assert.equal(r.answer.ok,false)
    assert.match(r.answer.error,/nothing to keep/)
    assert.equal(r.writes.length,0)
  }
  const refresh = await run({type:'success',refresh:'synthetic-refresh',access:'synthetic-access',expires:123})
  assert.equal(refresh.writes[0].value.refresh,'synthetic-refresh')
  const key = await run({type:'success',key:'synthetic-key',metadata:{project:'test'}})
  assert.deepEqual(key.writes[0].value,{type:'api',key:'synthetic-key',metadata:{project:'test'}})
  const redirected = await run({type:'success',provider:'other',access:'synthetic-access'})
  assert.equal(redirected.answer.provider,'other')
  assert.equal(redirected.writes[0].key,'other#new')
  const failure = await run({type:'failed',error:'test failure'})
  assert.deepEqual(failure.answer,{ok:false,error:'test failure'})
  assert.equal(failure.writes.length,0)
  console.log('9 synthetic production callback cases passed; no real accounts or network')
})().catch(error => { console.error(error);process.exitCode=1 })
