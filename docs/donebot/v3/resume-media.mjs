import { readFile, writeFile } from 'node:fs/promises';
import { setTimeout } from 'node:timers/promises';

let input = '';
for await (const chunk of process.stdin) input += chunk;
const { origin, credential, journalPath, maxPolls = 5 } = JSON.parse(input);
const endpoint = new URL(origin);
if (endpoint.protocol !== 'http:' || endpoint.hostname !== '127.0.0.1' || !endpoint.port || endpoint.pathname !== '/' || endpoint.username || endpoint.password) throw new Error('Only an explicit local fixture is allowed');
const journal = JSON.parse(await readFile(journalPath, 'utf8'));
if (!/^\/v1\/(videos|images\/tasks|music\/tasks)\/fixture-task-[a-f0-9-]+$/.test(journal.poll_path)) throw new Error('Invalid fixture poll path');
for (let attempt = 0; attempt < maxPolls && journal.status !== 'completed'; attempt += 1) {
  const response = await fetch(`${endpoint.origin}${journal.poll_path}`, {
    headers: { authorization: `Bearer ${credential}` }, redirect: 'error', signal: AbortSignal.timeout(5000),
  });
  if (!response.ok) throw new Error(`Fixture poll failed: ${response.status}`);
  const task = await response.json();
  if (task.id !== journal.id || task.receipt_id !== journal.receipt_id) throw new Error('Fixture task identity changed');
  journal.status = task.status;
  if (task.result) journal.attachment_ref = task.result.attachment_ref;
  await writeFile(journalPath, `${JSON.stringify(journal)}\n`, { mode: 0o600 });
  await setTimeout(2);
}
process.stdout.write(`${JSON.stringify({ id: journal.id, status: journal.status, receipt_id: journal.receipt_id })}\n`);
