import { describe, it, expect, vi } from 'vitest'
import donebot, { type StorageSettings } from '../donebot'
import { apiClient } from '../../client'
vi.mock('../../client', () => ({ apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() } }))
describe('DoneBot admin API', () => {
 it('deletes a client release through the version-specific admin endpoint', async () => {
  vi.mocked(apiClient.delete).mockResolvedValue({ data: { deleted: true } })
  await donebot.deleteClientRelease('0.1.0')
  expect(apiClient.delete).toHaveBeenLastCalledWith('/admin/donebot/releases/0.1.0')
 })
 it('allows installer uploads to run for one hour and reports transport progress', async () => {
  vi.mocked(apiClient.post).mockResolvedValue({ data: { version: '0.1.0' } })
  const form = new FormData()
  const progress = vi.fn()
  await donebot.uploadClientRelease(form, progress)
  expect(apiClient.post).toHaveBeenLastCalledWith('/admin/donebot/releases/upload', form, {
   headers: { 'Content-Type': 'multipart/form-data' }, timeout: 3_600_000, onUploadProgress: progress
  })
 })
 it('loads public web tool status and writes replacement or clear commands separately', async () => {
  const status = { enabled: true, key_configured: true }
  vi.mocked(apiClient.get).mockResolvedValue({ data: status })
  vi.mocked(apiClient.put).mockResolvedValue({ data: status })
  expect(await donebot.webTools()).toEqual(status)
  expect(apiClient.get).toHaveBeenCalledWith('/admin/donebot/web-tools')
  expect(await donebot.saveWebTools({ enabled: true, api_key: 'fixture-secret' })).toEqual(status)
  expect(apiClient.put).toHaveBeenLastCalledWith('/admin/donebot/web-tools', { enabled: true, api_key: 'fixture-secret' })
  await donebot.saveWebTools({ enabled: false, clear_api_key: true })
  expect(apiClient.put).toHaveBeenLastCalledWith('/admin/donebot/web-tools', { enabled: false, clear_api_key: true })
 })
 it('loads and saves the two shared storage policies', async () => {
  const policies = { upload: { driver: 'local' }, generated: { driver: 's3' } } as StorageSettings
  vi.mocked(apiClient.get).mockResolvedValue({ data: policies })
  vi.mocked(apiClient.put).mockResolvedValue({ data: policies })
  expect(await donebot.storage()).toEqual(policies)
  expect(apiClient.get).toHaveBeenCalledWith('/admin/donebot/storage')
  expect(await donebot.saveStorage(policies)).toEqual(policies)
  expect(apiClient.put).toHaveBeenCalledWith('/admin/donebot/storage', policies)
 })
 it('saves model selection and current pricing with one configuration request', async () => {
  vi.mocked(apiClient.put).mockResolvedValue({ data: { models: [] } })
  const input = { models: [{ model: 'gpt-test', enabled: false, pricing: { rate_multiplier: 1.25 } }] }
  await donebot.saveConfiguration('llm', input)
  expect(apiClient.put).toHaveBeenLastCalledWith('/admin/donebot/llm/configuration', input)
 })
 it('uses explicit add-only sync and installation revocation endpoints', async () => {
  vi.mocked(apiClient.post).mockResolvedValue({ data: {} });vi.mocked(apiClient.delete).mockResolvedValue({ data: null })
  await donebot.sync('images');await donebot.revoke(7)
  expect(apiClient.post).toHaveBeenCalledWith('/admin/donebot/images/models/sync')
  expect(apiClient.delete).toHaveBeenCalledWith('/admin/donebot/installs/7')
 })
 it('removes a cloud node through its dedicated admin endpoint', async () => {
  vi.mocked(apiClient.delete).mockResolvedValue({ data: { deleted: true } })
  await donebot.deleteCloudNode('fixture-node')
  expect(apiClient.delete).toHaveBeenLastCalledWith('/admin/donebot/cloud/nodes/fixture-node')
 })
})
