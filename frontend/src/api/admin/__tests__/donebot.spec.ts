import { describe, it, expect, vi } from 'vitest'
import donebot, { type StorageSettings } from '../donebot'
import { apiClient } from '../../client'
vi.mock('../../client', () => ({ apiClient: { get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() } }))
describe('DoneBot admin API', () => {
 it('loads and saves the two shared storage policies', async () => {
  const policies = { upload: { driver: 'local' }, generated: { driver: 's3' } } as StorageSettings
  vi.mocked(apiClient.get).mockResolvedValue({ data: policies })
  vi.mocked(apiClient.put).mockResolvedValue({ data: policies })
  expect(await donebot.storage()).toEqual(policies)
  expect(apiClient.get).toHaveBeenCalledWith('/admin/donebot/storage')
  expect(await donebot.saveStorage(policies)).toEqual(policies)
  expect(apiClient.put).toHaveBeenCalledWith('/admin/donebot/storage', policies)
 })
 it('saves model selection and group pricing separately', async () => {
  vi.mocked(apiClient.put).mockResolvedValue({ data: { models: [] } })
  await donebot.models('llm', [{ model: 'gpt-test', enabled: false }])
  expect(apiClient.put).toHaveBeenCalledWith('/admin/donebot/llm/models', { models: [{ model: 'gpt-test', enabled: false }] })
  await donebot.pricing('llm', { rate_multiplier: 1.25 })
  expect(apiClient.put).toHaveBeenLastCalledWith('/admin/donebot/llm/pricing', { rate_multiplier: 1.25 })
 })
 it('uses explicit add-only sync and installation revocation endpoints', async () => {
  vi.mocked(apiClient.post).mockResolvedValue({ data: {} });vi.mocked(apiClient.delete).mockResolvedValue({ data: null })
  await donebot.sync('images');await donebot.revoke(7)
  expect(apiClient.post).toHaveBeenCalledWith('/admin/donebot/images/models/sync')
  expect(apiClient.delete).toHaveBeenCalledWith('/admin/donebot/installs/7')
 })
})
