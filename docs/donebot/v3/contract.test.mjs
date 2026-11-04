import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { request } from 'node:http';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { createFixtureGateway, fixtureCatalog, protocolCases } from './fake-gateway.mjs';

const installationScopes = new WeakMap();
const ROOT = fileURLToPath(new URL('.', import.meta.url));
const schema = JSON.parse(await readFile(new URL('./catalog.schema.json', import.meta.url), 'utf8'));
function validate(value, rule, at = '$') {
  if (rule.$ref) return validate(value, rule.$ref.slice(2).split('/').reduce((item, part) => item[part], schema), at);
  if (Object.hasOwn(rule, 'const')) assert.deepEqual(value, rule.const, `${at} const`);
  if (rule.enum) assert.ok(rule.enum.some((item) => JSON.stringify(item) === JSON.stringify(value)), `${at} enum`);
  const types = { object: value !== null && typeof value === 'object' && !Array.isArray(value), array: Array.isArray(value), null: value === null, string: typeof value === 'string', boolean: typeof value === 'boolean', integer: Number.isInteger(value), number: typeof value === 'number' && Number.isFinite(value) };
  if (rule.type) assert.ok([rule.type].flat().some((type) => types[type]), `${at} type`);
  if (typeof value === 'string') {
    if (rule.minLength !== undefined) assert.ok(value.length >= rule.minLength, `${at} length`);
	if (rule.maxLength !== undefined) assert.ok(value.length <= rule.maxLength, `${at} length`);
    if (rule.pattern) assert.match(value, new RegExp(rule.pattern), `${at} pattern`);
  }
  if (typeof value === 'number' && rule.minimum !== undefined) assert.ok(value >= rule.minimum, `${at} minimum`);
  if (typeof value === 'number' && rule.maximum !== undefined) assert.ok(value <= rule.maximum, `${at} maximum`);
  if (Array.isArray(value)) {
    if (rule.minItems !== undefined) assert.ok(value.length >= rule.minItems, `${at} minItems`);
    if (rule.maxItems !== undefined) assert.ok(value.length <= rule.maxItems, `${at} maxItems`);
    if (rule.uniqueItems) assert.equal(new Set(value.map((item) => JSON.stringify(item))).size, value.length, `${at} uniqueItems`);
    if (rule.items) value.forEach((item, index) => validate(item, rule.items, `${at}[${index}]`));
  }
  if (types.object) {
    for (const name of rule.required ?? []) assert.ok(Object.hasOwn(value, name), `${at}.${name} required`);
    for (const [name, item] of Object.entries(value)) {
      if (rule.properties?.[name]) validate(item, rule.properties[name], `${at}.${name}`);
      else if (rule.additionalProperties === false) assert.fail(`${at}.${name} unexpected`);
      else if (typeof rule.additionalProperties === 'object') validate(item, rule.additionalProperties, `${at}.${name}`);
    }
  }
  if (rule.allOf) rule.allOf.forEach((item) => validate(value, item, at));
  if (rule.oneOf) {
    const matches = rule.oneOf.filter((item) => { try { validate(value, item, at); return true; } catch { return false; } });
    assert.equal(matches.length, 1, `${at} oneOf`);
  }
  if (rule.if) {
    let matches = true;
    try { validate(value, rule.if, at); } catch { matches = false; }
    if (matches && rule.then) validate(value, rule.then, at);
    if (!matches && rule.else) validate(value, rule.else, at);
  }
}
async function fixture(t, options) {
  const gateway = await createFixtureGateway(options);
  t.after(() => gateway.close());
  return gateway;
}
async function call(gateway, path, { credential = 'fixture-access-a', method = 'GET', body, headers = {} } = {}) {
  return fetch(`${gateway.url}${path}`, { method, headers: { authorization: `Bearer ${credential}`, ...(installationScopes.get(gateway)?.get(credential) ? { 'x-donebot-installation': installationScopes.get(gateway).get(credential) } : {}), ...(body ? { 'content-type': 'application/json' } : {}), ...headers }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.timeout(5000), redirect: 'error' });
}
async function claim(gateway, access = 'fixture-access-a', installationId = randomUUID()) {
  const response = await call(gateway, '/api/v1/donebot/installations/claim', { credential: access, method: 'POST', body: { installation_id: installationId, install_name: 'Fixture test', fingerprint: 'fixture-fingerprint' } });
  assert.equal(response.status, 201);
  if (!installationScopes.has(gateway)) installationScopes.set(gateway, new Map());
  installationScopes.get(gateway).set(access, installationId);
  return { ...(await response.json()), installationId };
}
function message(protocol, extra = {}) {
  if (protocol === 'chat') return { model: 'fixture-chat', messages: [{ role: 'user', content: 'hello' }], ...extra };
  if (protocol === 'responses') return { model: 'fixture-responses', input: [{ role: 'user', content: 'hello' }], ...extra };
  if (protocol === 'messages') return { model: 'fixture-messages', max_tokens: 128, messages: [{ role: 'user', content: 'hello' }], ...extra };
  return { contents: [{ role: 'user', parts: [{ text: 'hello' }] }], ...extra };
}
function tools(protocol) {
  if (protocol === 'chat') return [{ type: 'function', function: { name: 'fixture_echo', parameters: { type: 'object' } } }];
  if (protocol === 'responses') return [{ type: 'function', name: 'fixture_echo', parameters: { type: 'object' } }];
  if (protocol === 'messages') return [{ name: 'fixture_echo', input_schema: { type: 'object' } }];
  return [{ functionDeclarations: [{ name: 'fixture_echo', parameters: { type: 'object' } }] }];
}
function toolResult(protocol, response) {
  if (protocol === 'chat') return { messages: [{ role: 'assistant', ...response.choices[0].message }, { role: 'tool', tool_call_id: 'fixture-tool', content: 'hello' }] };
  if (protocol === 'responses') return { input: [{ type: 'function_call_output', call_id: 'fixture-tool', output: 'hello' }] };
  if (protocol === 'messages') return { messages: [{ role: 'assistant', content: response.content }, { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'fixture-tool', content: 'hello' }] }] };
  return { contents: [{ role: 'user', parts: [{ functionResponse: { name: 'fixture_echo', response: { text: 'hello' } } }] }] };
}
const output = (protocol, result) => protocol === 'chat' ? result.choices[0].message.content
  : protocol === 'responses' ? result.output.find((item) => item.type === 'message')?.content[0].text
    : protocol === 'messages' ? result.content.find((item) => item.type === 'text')?.text : result.candidates[0].content.parts[0].text;

test('v3 schema accepts the fixture and rejects unsupported, mixed-currency and leaked-metadata catalogs', () => {
  validate(fixtureCatalog, schema);
  const invalid = structuredClone(fixtureCatalog);
  invalid.models[0].pricing.currency = 'USD';
  assert.throws(() => validate(invalid, schema));
  invalid.models[0].pricing.currency = 'CNY';
  invalid.models[0].account_id = 123;
  assert.throws(() => validate(invalid, schema));
  delete invalid.models[0].account_id;
  invalid.models[0].capabilities.reasoning.supported = false;
  assert.throws(() => validate(invalid, schema));
  for (const model of fixtureCatalog.models) {
    const reasoning = model.capabilities.reasoning;
    if (reasoning.default !== null) assert.ok(reasoning.levels.includes(reasoning.default));
    assert.equal(model.pricing.currency, 'CNY');
    if (model.media_types.includes('text')) assert.equal(model.pricing.input_per_million, 1);
  }
});

test('input modalities are optional exact declarations independent of generated media', () => {
  const catalog = structuredClone(fixtureCatalog);
  for (const model of catalog.models) delete model.capabilities.input_modalities;
  validate(catalog, schema);
  const model = catalog.models.find((item) => item.media_types.includes('text'));
  model.capabilities.input_modalities = ['text', 'image', 'audio', 'video'];
  validate(catalog, schema);
  assert.deepEqual(model.media_types, ['text']);
  for (const value of [null, [], ['image', 'image'], ['file'], ['Image'], ['text', 1], { image: true }]) {
    model.capabilities.input_modalities = value;
    assert.throws(() => validate(catalog, schema));
  }
});

test('media metadata uses exact adapter contracts and rejects URL, path, selector and adapter injection', () => {
  for (const change of [
    (protocol) => { protocol.adapter = 'unrecognized'; },
    (protocol) => { protocol.submit.path = 'https://attacker.invalid/collect'; },
    (protocol) => { protocol.poll.path = '/api/v1/auth/me'; },
    (protocol) => { protocol.poll.status_paths = ['authorization']; },
    (protocol) => { protocol.poll.path = '/v1/tasks/{task_id}?key={credential}'; },
  ]) {
    const invalid = structuredClone(fixtureCatalog);
    change(invalid.models.find((model) => model.id === 'fixture-image').media_parameters.protocol);
    assert.throws(() => validate(invalid, schema));
  }
  for (const model of fixtureCatalog.models.filter((item) => item.media_parameters)) {
    const { protocol } = model.media_parameters;
    assert.ok(['openai.images.v1', 'apimart.images.v1', 'apimart.music.v1', 'apimart.speech.v1', 'seedance.videos.v1'].includes(protocol.adapter));
    assert.equal(protocol.submit.method, 'POST');
    assert.equal(model.pricing.currency, 'CNY');
  }
});

test('native media fixture matches APIMart task envelopes, Seedance content/status and binary speech', async (t) => {
  const gateway = await fixture(t, { nativeMedia: true });
  const { credential } = await claim(gateway);
  const select = (document, path) => path.split('.').reduce((value, segment) => value?.[segment], document);
  for (const id of ['fixture-image', 'fixture-music', 'fixture-video']) {
    const model = fixtureCatalog.models.find((item) => item.id === id);
    const { protocol } = model.media_parameters;
    const request = { model: id, ...(id === 'fixture-video' ? { content: [{ type: 'text', text: 'fixture-only' }], resolution: '720p', duration: 5 } : { prompt: 'fixture-only', ...(id === 'fixture-music' ? { version: 'v6' } : {}) }) };
    const submitted = await call(gateway, protocol.submit.path, { credential, method: protocol.submit.method, body: request, headers: { 'idempotency-key': randomUUID() } });
    assert.equal(submitted.status, 202);
    const response = await submitted.json();
    const taskId = select(response, protocol.task_id_path);
    assert.match(taskId, /^fixture-task-/);
    const path = protocol.poll.path.replace('{task_id}', encodeURIComponent(taskId));
    const pending = await (await call(gateway, path, { credential })).json();
    const status = protocol.poll.status_paths.map((selector) => select(pending, selector)).find((value) => value !== undefined);
    assert.equal(protocol.poll.completed_statuses.includes(status), false);
    const completed = await (await call(gateway, path, { credential })).json();
    const finalStatus = protocol.poll.status_paths.map((selector) => select(completed, selector)).find((value) => value !== undefined);
    assert.equal(protocol.poll.completed_statuses.includes(finalStatus), true);
    assert.ok(protocol.poll.result_paths.some((selector) => select(completed, selector) !== undefined));
    if (protocol.content) {
      const content = await call(gateway, protocol.content.path.replace('{task_id}', encodeURIComponent(taskId)), { credential });
      assert.equal(content.headers.get('content-type'), 'video/mp4');
      assert.equal(await content.text(), 'fixture-only-video');
    }
  }
  const speech = fixtureCatalog.models.find((item) => item.id === 'fixture-speech').media_parameters.protocol;
  assert.equal(speech.submit.response_kind, 'binary');
  assert.equal(Object.hasOwn(speech, 'poll'), false);
  const audio = await call(gateway, speech.submit.path, { credential, method: 'POST', body: { model: 'fixture-speech', input: 'fixture', voice: 'alloy', response_format: 'wav' } });
  assert.equal(audio.headers.get('content-type'), 'audio/wav');
  assert.match(await audio.text(), /^RIFF/);
});

test('fixture login, claim-once, safe installation list and account summary', async (t) => {
  const gateway = await fixture(t);
  const login = await call(gateway, '/api/v1/auth/login', { method: 'POST', body: { fixture_user: 'a', fixture_password: 'fixture-only' } });
  assert.equal(login.status, 200);
  const access = (await login.json()).access_token;
  const installed = await claim(gateway, access);
  assert.match(installed.credential, /^sk-donebot-[a-f0-9]{64}$/);
  const replay = await call(gateway, '/api/v1/donebot/installations/claim', { credential: access, method: 'POST', body: { installation_id: installed.installationId, install_name: 'Fixture test', fingerprint: 'fixture-fingerprint' } });
  assert.equal(replay.status, 200);
  assert.equal(replay.headers.get('cache-control'), 'no-store');
  assert.equal(Object.hasOwn(await replay.json(), 'credential'), false);
  const list = await (await call(gateway, '/api/v1/donebot/installations')).json();
  assert.equal(list.installations.length, 1);
  assert.equal(JSON.stringify(list).includes(installed.credential), false);
  const account = await (await call(gateway, '/api/v1/donebot/account')).json();
  assert.deepEqual(account, { currency: 'CNY', available_cents: 10000 });
});

test('catalog scope authentication precedes ETag and versions change only on revision', async (t) => {
  const gateway = await fixture(t);
  const a = await claim(gateway);
  const b = await claim(gateway, 'fixture-access-b');
  const read = (installation, access, etag) => call(gateway, '/api/v1/donebot/models', { credential: access, headers: { 'x-donebot-installation': installation, ...(etag ? { 'if-none-match': etag } : {}) } });
  const first = await read(a.installationId, 'fixture-access-a');
  assert.equal(first.status, 200);
  validate(await first.json(), schema);
  assert.equal(first.headers.get('cache-control'), 'private, no-cache');
  const etag = first.headers.get('etag');
  assert.equal((await read(a.installationId, 'fixture-access-a', etag)).status, 304);
  assert.equal((await read(a.installationId, 'fixture-access-b', etag)).status, 403);
  const other = await read(b.installationId, 'fixture-access-b', etag);
  assert.equal(other.status, 200);
  assert.notEqual(other.headers.get('etag'), etag);
  const bCatalog = await other.json();
  assert.equal(bCatalog.models.length, 1);
  gateway.reviseCatalog();
  const revised = await read(a.installationId, 'fixture-access-a', etag);
  assert.equal(revised.status, 200);
  assert.notEqual(revised.headers.get('etag'), etag);
});

for (const protocol of protocolCases) {
  test(`${protocol.name}: native non-stream response and tool result roundtrip`, async (t) => {
    const gateway = await fixture(t);
    const { credential } = await claim(gateway);
    const plain = await call(gateway, protocol.path, { credential, method: 'POST', body: message(protocol.name) });
    assert.equal(plain.status, 200);
    assert.equal(output(protocol.name, await plain.json()), 'fixture-ok');
    const first = await call(gateway, protocol.path, { credential, method: 'POST', body: message(protocol.name, { tools: tools(protocol.name) }) });
    const tool = await first.json();
    assert.ok(JSON.stringify(tool).includes('fixture_echo'));
    const second = await call(gateway, protocol.path, { credential, method: 'POST', body: message(protocol.name, { tools: tools(protocol.name), ...toolResult(protocol.name, tool) }) });
    assert.equal(output(protocol.name, await second.json()), 'fixture-ok');
  });
  test(`${protocol.name}: native SSE events stay ordered, tool calls survive, receipts stay outside the stream`, async (t) => {
    const gateway = await fixture(t);
    const { credential } = await claim(gateway);
    const path = protocol.name === 'gemini' ? protocol.path.replace(':generateContent', ':streamGenerateContent') : protocol.path;
    const response = await call(gateway, path, { credential, method: 'POST', body: message(protocol.name, { stream: true, tools: tools(protocol.name) }), headers: { 'anthropic-beta': 'fixture-beta-1m' } });
    assert.equal(response.status, 200);
    assert.match(response.headers.get('content-type'), /text\/event-stream/);
    const chunks = [];
    for await (const chunk of response.body) chunks.push(Buffer.from(chunk));
    const stream = Buffer.concat(chunks).toString('utf8');
    assert.ok(chunks.length > 1);
    assert.match(stream, /fixture_echo/);
    assert.doesNotMatch(stream, /event: receipt|fixture-receipt-/);
    if (protocol.name === 'chat') assert.ok(stream.indexOf('tool_calls') < stream.indexOf('[DONE]'));
    if (protocol.name === 'responses') {
      assert.ok(stream.indexOf('response.created') < stream.indexOf('response.completed'));
      assert.match(stream, /fixture-reasoning/);
    }
    if (protocol.name === 'messages') {
      assert.ok(stream.indexOf('message_start') < stream.indexOf('message_stop'));
      assert.match(stream, /fixture-signature/);
      assert.equal(gateway.observed.at(-1).anthropicBeta, 'fixture-beta-1m');
    }
    if (protocol.name === 'gemini') assert.match(stream, /functionCall/);
    const receiptId = response.headers.get('x-donebot-receipt-id');
    assert.match(receiptId, /^fixture-receipt-/);
    const receipt = await (await call(gateway, `/api/v1/donebot/receipts/${receiptId}`)).json();
    assert.equal(receipt.request_id, response.headers.get('x-request-id'));
    assert.equal(receipt.currency, 'CNY');
    assert.equal(receipt.actual_cents, 0.02);
    const plain = await call(gateway, path, { credential, method: 'POST', body: message(protocol.name, { stream: true }) });
    assert.equal(plain.status, 200);
    const plainStream = await plain.text();
    assert.match(plainStream, /fixture-ok/);
    assert.doesNotMatch(plainStream, /fixture_echo|event: receipt/);
  });
}

test('reasoning uses each native field, omits unselected effort and reports requested/effective downgrade', async (t) => {
  const gateway = await fixture(t, { maxEffort: 'medium' });
  const { credential } = await claim(gateway);
  const selections = { chat: { reasoning_effort: 'high' }, responses: { reasoning: { effort: 'high' } }, messages: { output_config: { effort: 'high' } }, gemini: { generationConfig: { thinkingConfig: { thinkingBudget: 4096 } } } };
  for (const protocol of protocolCases) {
    const plain = await call(gateway, protocol.path, { credential, method: 'POST', body: message(protocol.name) });
    assert.equal(plain.status, 200);
    const plainReceipt = await (await call(gateway, `/api/v1/donebot/receipts/${plain.headers.get('x-donebot-receipt-id')}`)).json();
    assert.equal(plainReceipt.requested_reasoning, null);
    assert.equal(plainReceipt.effective_reasoning, null);
    const response = await call(gateway, protocol.path, { credential, method: 'POST', body: message(protocol.name, selections[protocol.name]) });
    assert.equal(response.status, 200);
    const receipt = await (await call(gateway, `/api/v1/donebot/receipts/${response.headers.get('x-donebot-receipt-id')}`)).json();
    assert.equal(receipt.requested_reasoning, 'high');
    assert.equal(receipt.effective_reasoning, protocol.name === 'gemini' ? 'low' : 'medium');
    assert.deepEqual(gateway.observed.at(-1).body, message(protocol.name, selections[protocol.name]));
  }
  for (const selection of ['high', 'invented']) {
    const response = await call(gateway, '/v1/chat/completions', { credential, method: 'POST', body: message('chat', { model: 'fixture-no-reasoning', reasoning_effort: selection }) });
    assert.equal(response.status, 400);
  }
  const allowed = await call(gateway, '/v1/chat/completions', { credential, method: 'POST', body: message('chat', { model: 'fixture-no-reasoning' }) });
  assert.equal(allowed.status, 200);
});

test('key rotation invalidates old key immediately; ownership and revocation remain enforced', async (t) => {
  const gateway = await fixture(t);
  const installed = await claim(gateway);
  const path = `/api/v1/donebot/installations/${installed.installationId}`;
  assert.equal((await call(gateway, `${path}/rotate`, { credential: 'fixture-access-b', method: 'POST' })).status, 404);
  const rotated = await (await call(gateway, `${path}/rotate`, { method: 'POST' })).json();
  assert.notEqual(rotated.credential, installed.credential);
  assert.equal((await call(gateway, '/v1/chat/completions', { credential: installed.credential, method: 'POST', body: message('chat') })).status, 401);
  assert.equal((await call(gateway, '/v1/chat/completions', { credential: rotated.credential, method: 'POST', body: message('chat') })).status, 200);
  assert.equal((await call(gateway, path, { method: 'DELETE' })).status, 200);
  assert.equal((await call(gateway, '/v1/chat/completions', { credential: rotated.credential, method: 'POST', body: message('chat') })).status, 401);
});

test('402 never creates a task; 429 preserves retry hint without model fallback', async (t) => {
  const gateway = await fixture(t);
  const { credential } = await claim(gateway);
  for (const status of [402, 429]) {
    const response = await call(gateway, '/v1/videos', { credential, method: 'POST', body: { model: 'fixture-video', prompt: 'fixture' }, headers: { 'idempotency-key': randomUUID(), 'x-fixture-failure': String(status) } });
    assert.equal(response.status, status);
    if (status === 429) assert.equal(response.headers.get('retry-after'), '2');
  }
  assert.equal(gateway.taskCount(), 0);
});

async function resume(input) {
  const child = spawn(process.execPath, [join(ROOT, 'resume-media.mjs')], { cwd: ROOT, env: {}, stdio: ['pipe', 'pipe', 'pipe'] });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  child.stdin.end(JSON.stringify(input));
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
  assert.equal(code, 0, stderr);
  assert.equal(stdout.includes(input.credential), false);
  assert.equal(stderr.includes(input.credential), false);
  return JSON.parse(stdout);
}

test('fixture media idempotency and task journal survive a real poller process restart and key rotation without storing a key', async (t) => {
  const gateway = await fixture(t);
  const installed = await claim(gateway);
  const operation = randomUUID();
  const headers = { 'idempotency-key': operation };
  const request = { model: 'fixture-video', prompt: 'fixture' };
  const first = await call(gateway, '/v1/videos', { credential: installed.credential, method: 'POST', body: request, headers });
  assert.equal(first.status, 202);
  const accepted = await first.json();
  const dir = await mkdtemp(join(ROOT, '.fixture-run-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const journalPath = join(dir, 'task.json');
  await writeFile(journalPath, `${JSON.stringify({ ...accepted, operation_id: operation, installation_id: installed.installationId })}\n`, { mode: 0o600 });
  const paused = await resume({ origin: gateway.url, credential: installed.credential, journalPath, maxPolls: 1 });
  assert.equal(paused.status, 'queued');
  const rotated = await (await call(gateway, `/api/v1/donebot/installations/${installed.installationId}/rotate`, { method: 'POST' })).json();
  const replay = await call(gateway, '/v1/videos', { credential: rotated.credential, method: 'POST', body: request, headers });
  assert.deepEqual(await replay.json(), accepted);
  const conflict = await call(gateway, '/v1/videos', { credential: rotated.credential, method: 'POST', body: { ...request, prompt: 'changed' }, headers });
  assert.equal(conflict.status, 409);
  const recovered = await resume({ origin: gateway.url, credential: rotated.credential, journalPath });
  assert.equal(recovered.status, 'completed');
  assert.equal(recovered.id, accepted.id);
  assert.equal(gateway.taskCount(), 1);
  const disk = await readFile(journalPath, 'utf8');
  assert.equal(disk.includes('sk-donebot-'), false);
  assert.equal(disk.includes('authorization'), false);
  assert.match(disk, /fixture-attachment:/);
});

test('fixture image/music tasks and synchronous speech keep native media routes separate from text', async (t) => {
  const gateway = await fixture(t);
  const { credential } = await claim(gateway);
  for (const path of ['/v1/images/generations', '/v1/images/edits', '/v1/videos/edits', '/v1/videos/extensions', '/v1/music/generations']) {
    const response = await call(gateway, path, { credential, method: 'POST', body: { model: 'fixture-media' }, headers: { 'idempotency-key': randomUUID() } });
    assert.equal(response.status, 202);
    const task = await response.json();
    assert.match(task.poll_path, /^\/v1\//);
  }
  const speech = await call(gateway, '/v1/audio/speech', { credential, method: 'POST', body: { model: 'fixture-speech', input: 'hello' } });
  assert.equal(speech.headers.get('content-type'), 'audio/wav');
  assert.match(await speech.text(), /^RIFF/);
  assert.equal(gateway.observed.length, 0);
});

test('fixture surface binds only IPv4 loopback and refuses a non-loopback Host without CORS', async (t) => {
  const gateway = await fixture(t);
  assert.equal(new URL(gateway.url).hostname, '127.0.0.1');
  const response = await new Promise((resolve, reject) => {
    const req = request(`${gateway.url}/api/v1/donebot/account`, { headers: { host: 'attacker.example', origin: 'https://attacker.example', authorization: 'Bearer fixture-access-a' } }, (res) => {
      res.resume();
      res.on('end', () => resolve({ status: res.statusCode, cors: res.headers['access-control-allow-origin'] }));
    });
    req.on('error', reject);
    req.end();
  });
  assert.equal(response.status, 403);
  assert.equal(response.cors, undefined);
});

test('text receipt scope is installation-bound and missing settlement returns 404 with Retry-After', async (t) => {
  const gateway = await fixture(t);
  const installed = await claim(gateway);
  const response = await call(gateway, '/v1/chat/completions', { credential: installed.credential, method: 'POST', body: message('chat') });
  const receiptPath = `/api/v1/donebot/receipts/${response.headers.get('x-donebot-receipt-id')}`;
  const wrong = await call(gateway, receiptPath, { headers: { 'x-donebot-installation': randomUUID() } });
  assert.equal(wrong.status, 404);
  const good = await call(gateway, receiptPath);
  assert.equal(good.status, 200);
  assert.equal((await good.json()).actual_cents, 0.02);
  const unsettled = await call(gateway, '/api/v1/donebot/receipts/fixture-not-settled');
  assert.equal(unsettled.status, 404);
  assert.equal(unsettled.headers.get('retry-after'), '1');
});
