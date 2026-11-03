import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { setTimeout } from 'node:timers/promises';
import { pathToFileURL } from 'node:url';

export const fixtureCatalog = JSON.parse(readFileSync(new URL('./fixture.catalog.json', import.meta.url), 'utf8'));
const clone = (value) => structuredClone(value);
const digest = (value) => createHash('sha256').update(value).digest('hex');
const token = () => `sk-donebot-${randomBytes(32).toString('hex')}`;
const users = new Map([['fixture-access-a', 'fixture-user-a'], ['fixture-access-b', 'fixture-user-b']]);
const bearer = (req) => /^Bearer (.+)$/.exec(req.headers.authorization ?? '')?.[1];
const error = (res, status, code) => json(res, status, { error: { code, message: 'Fixture-only failure' } });
function json(res, status, body, headers = {}) {
  res.writeHead(status, { 'content-type': 'application/json', 'cache-control': 'no-store', ...headers });
  res.end(JSON.stringify(body));
}
async function body(req) {
  let raw = '';
  for await (const chunk of req) {
    raw += chunk;
    if (raw.length > 4 * 1024 * 1024) throw new Error('fixture body limit');
  }
  return raw ? JSON.parse(raw) : {};
}

export const protocolCases = [
  { name: 'chat', path: '/v1/chat/completions', model: 'fixture-chat', interface: 'openai.chat_completions' },
  { name: 'responses', path: '/v1/responses', model: 'fixture-responses', interface: 'openai.responses' },
  { name: 'messages', path: '/v1/messages', model: 'fixture-messages', interface: 'anthropic.messages' },
  { name: 'gemini', path: '/v1beta/models/fixture-gemini:generateContent', model: 'fixture-gemini', interface: 'gemini.generate_content' },
];

export function nativeResult(protocol, callTool) {
  if (protocol === 'chat') return {
    id: 'fixture-chat-response', object: 'chat.completion',
    choices: [{ index: 0, finish_reason: callTool ? 'tool_calls' : 'stop', message: callTool
      ? { role: 'assistant', content: null, tool_calls: [{ id: 'fixture-tool', type: 'function', function: { name: 'fixture_echo', arguments: '{"text":"hello"}' } }] }
      : { role: 'assistant', content: 'fixture-ok' } }],
    usage: { prompt_tokens: 4, completion_tokens: 2, total_tokens: 6 },
  };
  if (protocol === 'responses') return {
    id: 'fixture-responses-response', object: 'response', status: 'completed',
    output: [
      { id: 'fixture-reasoning', type: 'reasoning', summary: [{ type: 'summary_text', text: 'fixture reasoning' }] },
      callTool ? { id: 'fixture-call-item', type: 'function_call', call_id: 'fixture-tool', name: 'fixture_echo', arguments: '{"text":"hello"}' }
        : { id: 'fixture-message', type: 'message', role: 'assistant', content: [{ type: 'output_text', text: 'fixture-ok', annotations: [] }] },
    ], usage: { input_tokens: 4, output_tokens: 2, total_tokens: 6 },
  };
  if (protocol === 'messages') return {
    id: 'fixture-messages-response', type: 'message', role: 'assistant',
    content: [{ type: 'thinking', thinking: 'fixture thinking', signature: 'fixture-signature' }, callTool
      ? { type: 'tool_use', id: 'fixture-tool', name: 'fixture_echo', input: { text: 'hello' } }
      : { type: 'text', text: 'fixture-ok' }],
    stop_reason: callTool ? 'tool_use' : 'end_turn', usage: { input_tokens: 4, output_tokens: 2, cache_read_input_tokens: 1 },
  };
  return { candidates: [{ content: { role: 'model', parts: [callTool
    ? { functionCall: { name: 'fixture_echo', args: { text: 'hello' } } }
    : { text: 'fixture-ok' }] }, finishReason: 'STOP' }], usageMetadata: { promptTokenCount: 4, candidatesTokenCount: 2, totalTokenCount: 6 } };
}

export function nativeFrames(protocol, callTool) {
  const result = nativeResult(protocol, callTool);
  const event = (name, data) => `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;
  const data = (value) => `data: ${JSON.stringify(value)}\n\n`;
  if (protocol === 'chat') return [
    ': fixture heartbeat\n\n',
    data({ id: result.id, object: 'chat.completion.chunk', choices: [{ index: 0, delta: result.choices[0].message, finish_reason: null }] }),
    data({ id: result.id, object: 'chat.completion.chunk', choices: [{ index: 0, delta: {}, finish_reason: result.choices[0].finish_reason }], usage: result.usage }),
    'data: [DONE]\n\n',
  ];
  if (protocol === 'responses') return [
    event('response.created', { type: 'response.created', response: { id: result.id, status: 'in_progress' } }),
    ...result.output.flatMap((item, index) => [
      event('response.output_item.added', { type: 'response.output_item.added', output_index: index, item }),
      event('response.output_item.done', { type: 'response.output_item.done', output_index: index, item }),
    ]),
    event('response.completed', { type: 'response.completed', response: result }),
  ];
  if (protocol === 'messages') return [
    event('message_start', { type: 'message_start', message: { id: result.id, type: 'message', role: 'assistant', content: [] } }),
    ...result.content.flatMap((content_block, index) => [
      event('content_block_start', { type: 'content_block_start', index, content_block }),
      event('content_block_stop', { type: 'content_block_stop', index }),
    ]),
    event('message_delta', { type: 'message_delta', delta: { stop_reason: result.stop_reason }, usage: result.usage }),
    event('message_stop', { type: 'message_stop' }),
  ];
  return [': fixture heartbeat\n\n', data(result)];
}

function hasToolResult(protocol, value) {
  if (protocol === 'chat') return value.messages?.some((item) => item.role === 'tool');
  if (protocol === 'responses') return Array.isArray(value.input) && value.input.some((item) => item.type === 'function_call_output');
  if (protocol === 'messages') return value.messages?.some((item) => Array.isArray(item.content) && item.content.some((part) => part.type === 'tool_result'));
  return value.contents?.some((item) => item.parts?.some((part) => part.functionResponse));
}
function reasoningSelection(protocol, value) {
  if (protocol === 'chat') return value.reasoning_effort;
  if (protocol === 'responses') return value.reasoning?.effort;
  if (protocol === 'messages') return value.output_config?.effort;
  const budget = value.generationConfig?.thinkingConfig?.thinkingBudget;
  return budget === undefined ? undefined : budget === 1024 ? 'low' : budget === 4096 ? 'high' : 'invalid';
}

export async function createFixtureGateway({ maxEffort = null } = {}) {
  const installations = new Map();
  const credentials = new Map();
  const tasks = new Map();
  const operations = new Map();
  const receipts = new Map();
  const observed = [];
  let catalogRevision = 1;
  const metadata = (install) => ({ installation_id: install.id, name: install.name, status: install.status });
  const identity = (req) => {
    const install = installations.get(credentials.get(bearer(req)));
    return install?.status === 'active' ? install : null;
  };
  const server = createServer(async (req, res) => {
    try {
      if (req.headers.host !== `127.0.0.1:${server.address().port}`) return error(res, 403, 'FIXTURE_HOST_REJECTED');
      const url = new URL(req.url, 'http://fixture.invalid');
      const path = url.pathname;
      const user = users.get(bearer(req));
      if (path === '/api/v1/auth/login' && req.method === 'POST') {
        const value = await body(req);
        if (!['a', 'b'].includes(value.fixture_user) || value.fixture_password !== 'fixture-only') return error(res, 401, 'FIXTURE_LOGIN_INVALID');
        return json(res, 200, { access_token: `fixture-access-${value.fixture_user}`, token_type: 'Bearer' });
      }
      if (path.startsWith('/api/v1/donebot/') && !user) return error(res, 401, 'FIXTURE_ACCESS_REQUIRED');
      if (path === '/api/v1/donebot/installations/claim' && req.method === 'POST') {
        const value = await body(req);
        if (!/^[0-9a-f-]{36}$/i.test(value.installation_id ?? '') || !value.install_name || typeof value.fingerprint !== 'string') return error(res, 400, 'FIXTURE_CLAIM_INVALID');
        const existing = installations.get(value.installation_id);
        if (existing && existing.user !== user) return error(res, 409, 'FIXTURE_INSTALLATION_CONFLICT');
        if (existing) return json(res, 200, { install: metadata(existing) });
        const install = { id: value.installation_id, name: value.install_name, status: 'active', user };
        installations.set(install.id, install);
        const credential = token();
        credentials.set(credential, install.id);
        return json(res, 201, { install: metadata(install), credential });
      }
      if (path === '/api/v1/donebot/installations' && req.method === 'GET') return json(res, 200, { installations: [...installations.values()].filter((item) => item.user === user).map(metadata) });
      const installPath = /^\/api\/v1\/donebot\/installations\/([^/]+)(\/rotate)?$/.exec(path);
      if (installPath) {
        const install = installations.get(installPath[1]);
        if (!install || install.user !== user) return error(res, 404, 'FIXTURE_INSTALLATION_NOT_FOUND');
        if (installPath[2] && req.method === 'POST') {
          if (install.status !== 'active') return error(res, 409, 'FIXTURE_INSTALLATION_REVOKED');
          for (const [key, id] of credentials) if (id === install.id) credentials.delete(key);
          const credential = token();
          credentials.set(credential, install.id);
          return json(res, 200, { install: metadata(install), credential });
        }
        if (!installPath[2] && req.method === 'DELETE') {
          install.status = 'revoked';
          for (const [key, id] of credentials) if (id === install.id) credentials.delete(key);
          return json(res, 200, { install: metadata(install) });
        }
      }
      if (path === '/api/v1/donebot/models' && req.method === 'GET') {
        const install = installations.get(req.headers['x-donebot-installation']);
        if (!install || install.user !== user || install.status !== 'active') return error(res, 403, 'FIXTURE_SCOPE_REJECTED');
        const catalog = clone(fixtureCatalog);
        catalog.catalog_scope_id = `fixture-scope-${user}`;
        catalog.catalog_version = `fixture-v3-${catalogRevision}`;
        if (user === 'fixture-user-b') catalog.models = catalog.models.filter((item) => item.id === 'fixture-chat');
        const etag = `"${digest(JSON.stringify(catalog))}"`;
        const headers = { etag, vary: 'Authorization, X-DoneBot-Installation', 'cache-control': 'private, no-cache' };
        if (req.headers['if-none-match'] === etag) { res.writeHead(304, headers); return res.end(); }
        return json(res, 200, catalog, headers);
      }
      if (path === '/api/v1/donebot/account' && req.method === 'GET') return json(res, 200, { currency: 'CNY', available_cents: 10000 });
      const receiptPath = /^\/api\/v1\/donebot\/receipts\/([^/]+)$/.exec(path);
      if (receiptPath && req.method === 'GET') {
        const receipt = receipts.get(receiptPath[1]);
        if (!receipt) { res.setHeader('retry-after', '1'); return error(res, 404, 'FIXTURE_RECEIPT_NOT_SETTLED'); }
        const installation = installations.get(receipt.installation_id);
        if (installation?.user !== user || installation.status !== 'active' || req.headers['x-donebot-installation'] !== receipt.installation_id) return error(res, 404, 'FIXTURE_RECEIPT_NOT_FOUND');
        return json(res, 200, receipt);
      }
      const install = identity(req);
      if (!install) return error(res, 401, 'FIXTURE_KEY_INVALID');
      if (req.headers['x-fixture-failure'] === '402') return error(res, 402, 'FIXTURE_INSUFFICIENT_BALANCE');
      if (req.headers['x-fixture-failure'] === '429') { res.setHeader('retry-after', '2'); return error(res, 429, 'FIXTURE_RATE_LIMITED'); }
      const spec = protocolCases.find((item) => item.path === path || (item.name === 'gemini' && path === '/v1beta/models/fixture-gemini:streamGenerateContent'));
      if (spec && req.method === 'POST') {
        const value = await body(req);
        const model = fixtureCatalog.models.find((item) => item.id === (value.model ?? spec.model));
        if (!model || !model.interfaces.includes(spec.interface)) return error(res, 400, 'FIXTURE_MODEL_INTERFACE_INVALID');
        const requested = reasoningSelection(spec.name, value);
        const reasoning = model.capabilities.reasoning;
        if (requested !== undefined && (!reasoning.supported || !reasoning.levels.includes(requested))) return error(res, 400, 'FIXTURE_REASONING_UNSUPPORTED');
        const ordered = ['none', 'low', 'medium', 'high', 'xhigh', 'max'];
        const capped = maxEffort && ordered.indexOf(requested) > ordered.indexOf(maxEffort);
        const effective = capped ? reasoning.levels.filter((level) => ordered.indexOf(level) <= ordered.indexOf(maxEffort)).at(-1) : requested;
        const requestId = `fixture-request-${randomUUID()}`;
        const receiptId = `fixture-receipt-${randomUUID()}`;
        receipts.set(receiptId, { receipt_id: receiptId, request_id: requestId, installation_id: install.id, status: 'completed', currency: 'CNY', estimated_cents: 0.02, actual_cents: 0.02, requested_reasoning: requested ?? null, effective_reasoning: effective ?? null });
        observed.push({ path, body: clone(value), anthropicBeta: req.headers['anthropic-beta'], requestId, receiptId });
        res.setHeader('x-request-id', requestId);
        res.setHeader('x-donebot-receipt-id', receiptId);
        const callTool = Array.isArray(value.tools) && value.tools.length > 0 && !hasToolResult(spec.name, value);
        if (value.stream === true || path.endsWith(':streamGenerateContent')) {
          res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-store' });
          for (const frame of nativeFrames(spec.name, callTool)) { res.write(frame); await setTimeout(2); }
          return res.end();
        }
        return json(res, 200, nativeResult(spec.name, callTool));
      }
      const taskPath = /^\/v1\/(videos|images\/tasks|music\/tasks)\/([^/]+)$/.exec(path);
      if (taskPath && req.method === 'GET') {
        const task = tasks.get(taskPath[2]);
        if (!task || task.installation_id !== install.id) return error(res, 404, 'FIXTURE_TASK_NOT_FOUND');
        task.polls += 1;
        if (task.polls >= 2) {
          task.status = 'completed';
          const receipt = receipts.get(task.receipt_id);
          receipt.status = 'completed';
          receipt.actual_cents = 20;
        }
        return json(res, 200, { id: task.id, status: task.status, receipt_id: task.receipt_id, currency: 'CNY', actual_cents: task.status === 'completed' ? 20 : null, result: task.status === 'completed' ? { attachment_ref: `fixture-attachment:${task.id}` } : null });
      }
      const media = new Map([['/v1/videos', 'videos'], ['/v1/videos/edits', 'videos'], ['/v1/videos/extensions', 'videos'], ['/v1/images/generations', 'images/tasks'], ['/v1/images/edits', 'images/tasks'], ['/v1/music/generations', 'music/tasks']]);
      if (media.has(path) && req.method === 'POST') {
        const value = await body(req);
        const key = req.headers['idempotency-key'];
        if (!key) return error(res, 400, 'FIXTURE_IDEMPOTENCY_REQUIRED');
        const identityKey = `${install.id}:${key}`;
        const hash = digest(JSON.stringify({ path, value }));
        const previous = operations.get(identityKey);
        if (previous) {
          if (previous.hash !== hash) return error(res, 409, 'FIXTURE_IDEMPOTENCY_CONFLICT');
          return json(res, 200, previous.response);
        }
        const id = `fixture-task-${randomUUID()}`;
        const receiptId = `fixture-receipt-${randomUUID()}`;
        const task = { id, installation_id: install.id, receipt_id: receiptId, status: 'queued', polls: 0 };
        tasks.set(id, task);
        receipts.set(receiptId, { receipt_id: receiptId, installation_id: install.id, status: 'pending', currency: 'CNY', estimated_cents: 20, actual_cents: null });
        const response = { id, status: 'queued', receipt_id: receiptId, poll_path: `/v1/${media.get(path)}/${id}` };
        operations.set(identityKey, { hash, response });
        return json(res, 202, response);
      }
      if (path === '/v1/audio/speech' && req.method === 'POST') { await body(req); res.writeHead(200, { 'content-type': 'audio/wav' }); return res.end(Buffer.from('RIFFfixture-only-audio')); }
      return error(res, 404, 'FIXTURE_ROUTE_NOT_FOUND');
    } catch {
      if (!res.headersSent) error(res, 400, 'FIXTURE_REQUEST_INVALID');
      else res.destroy();
    }
  });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    observed,
    taskCount: () => tasks.size,
    reviseCatalog: () => { catalogRevision += 1; },
    close: () => new Promise((resolve) => { server.close(resolve); server.closeIdleConnections(); }),
  };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const gateway = await createFixtureGateway();
  process.stdout.write(`${JSON.stringify({ fixture: true, url: gateway.url, currency: 'CNY' })}\n`);
  const stop = () => gateway.close().then(() => process.exit(0));
  process.once('SIGINT', stop);
  process.once('SIGTERM', stop);
}
