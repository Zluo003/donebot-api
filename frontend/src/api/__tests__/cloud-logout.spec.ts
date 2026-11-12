import { beforeEach, expect, it, vi } from 'vitest'
const mock = vi.hoisted(() => ({ post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mock }))
beforeEach(() => { vi.resetAllMocks(); localStorage.clear(); localStorage.setItem('auth_token', 'fixture'); localStorage.setItem('refresh_token', 'fixture-refresh') })
it('revokes Cloud browser sessions before clearing gateway credentials on logout', async () => {
  const { logout } = await import('../auth')
  await logout()
  expect(mock.delete).toHaveBeenCalledWith('/donebot/cloud/browser-sessions', { timeout: 5000 })
  expect(mock.post).toHaveBeenCalledWith('/auth/logout', { refresh_token: 'fixture-refresh' })
  expect(mock.delete.mock.invocationCallOrder[0]).toBeLessThan(mock.post.mock.invocationCallOrder[0])
  expect(localStorage.getItem('auth_token')).toBeNull()
})
it('still clears local credentials if Cloud revocation is unavailable', async () => {
  mock.delete.mockRejectedValue(new Error('offline'))
  const { logout } = await import('../auth'); await logout()
  expect(mock.post).toHaveBeenCalled()
  expect(localStorage.getItem('auth_token')).toBeNull()
})
