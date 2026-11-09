import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'

const root = fileURLToPath(new URL('..', import.meta.url))
const entries = new Map()
function entry(document) {
  const pkg = document.package
  const content = JSON.stringify(document)
  return { id: pkg.id, release: pkg.release, name: pkg.name, summary: pkg.summary, category: pkg.category,
    kind: pkg.agents.length ? 'department' : 'bot', members: (pkg.agents.length ? pkg.agents : pkg.presets).map(item => item.name),
    skills: pkg.skills.entries.map(item => item.name), bytes: Buffer.byteLength(content), sha256: createHash('sha256').update(content).digest('hex'), published: true, created_at: new Date().toISOString(), document }
}
for (const kind of ['role', 'team']) {
  const document = JSON.parse(readFileSync(resolve(root, `../backend/internal/service/templateschema/testdata/${kind}.json`), 'utf8'))
  const release = entry(document)
  entries.set(`${release.id}/${release.release}`, release)
}
const server = await createServer({
  configFile: false, root, optimizeDeps: { entries: ['scripts/template-preview-entry.ts'] }, server: { host: '127.0.0.1', port: 0 },
  resolve: { alias: [
    { find: '@/components/layout/AppLayout.vue', replacement: resolve(root, 'scripts/TemplatePreviewLayout.vue') },
    { find: '@', replacement: resolve(root, 'src') },
    { find: 'vue-i18n', replacement: 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js' }
  ] },
  plugins: [vue(), {
    name: 'isolated-template-preview',
    configureServer(vite) {
      vite.middlewares.use(async (request, response, next) => {
        const path = new URL(request.url, 'http://127.0.0.1').pathname
        if (path === '/__templates') {
          response.setHeader('content-type', 'text/html')
          response.end(await vite.transformIndexHtml(path, '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>DoneBot 模板库预览</title></head><body><div id="app"></div><script type="module" src="/scripts/template-preview-entry.ts"></script></body></html>'))
          return
        }
        if (!path.startsWith('/api/')) { next(); return }
        response.setHeader('content-type', 'application/json')
        const send = (data, code = 200) => { response.statusCode = code; response.end(JSON.stringify(code === 200 ? { code: 0, data } : { code, message: data })) }
        const prefix = '/api/v1/admin/donebot/templates'
        if (!path.startsWith(prefix)) { send({}); return }
        try {
          let text = ''
          for await (const chunk of request) { text += chunk; if (Buffer.byteLength(text) > 4 * 1024 * 1024 + 1024) throw new Error('模板包过大') }
          const body = text ? JSON.parse(text) : null
          const suffix = decodeURIComponent(path.slice(prefix.length)).replace(/^\//, '')
          if (request.method === 'GET' && !suffix) return send({ templates: [...entries.values()] })
          if (request.method === 'POST') {
            const release = entry(body.document)
            if (suffix === 'preview') return send(release)
            const key = `${release.id}/${release.release}`
            if (entries.has(key)) return send('此版本已存在，请修改版本号后发布', 409)
            entries.set(key, release); return send(release)
          }
          const release = entries.get(suffix)
          if (!release) return send('模板不存在', 404)
          if (request.method === 'PATCH') { release.published = body.published; return send({ ok: true }) }
          return send(release)
        } catch (error) { send(error.message, 400) }
      })
    }
  }]
})
await server.listen()
console.log(new URL('/__templates', server.resolvedUrls.local[0]).href)
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, async () => { await server.close(); process.exit(0) })
