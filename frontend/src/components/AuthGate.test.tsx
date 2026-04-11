import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { PropsWithChildren } from 'react'
import AuthGate from './AuthGate'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => {
      const messages: Record<string, string> = {
        'common.loading': 'Loading',
        'auth.error': 'Invalid admin secret',
        'auth.subtitle': 'Sign in',
        'auth.placeholder': 'Enter admin secret',
        'auth.login': 'Login',
        'settings.adminSecret': 'Admin secret',
      }
      return messages[key] ?? key
    },
  }),
}))

function ProtectedContent({ children }: PropsWithChildren) {
  return <div>{children}</div>
}

describe('AuthGate', () => {
  const fetchMock = vi.fn()

  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
    localStorage.clear()
  })

  it('renders children only when admin health check succeeds', async () => {
    fetchMock.mockResolvedValueOnce(new Response('{}', { status: 200 }))

    render(
      <AuthGate>
        <ProtectedContent>Protected area</ProtectedContent>
      </AuthGate>,
    )

    await waitFor(() => {
      expect(screen.queryByText('Protected area')).not.toBeNull()
    })
  })

  it('does not authenticate when admin health returns service unavailable', async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({
      error: '管理鉴权暂时不可用，请稍后重试',
    }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
    }))

    render(
      <AuthGate>
        <ProtectedContent>Protected area</ProtectedContent>
      </AuthGate>,
    )

    await waitFor(() => {
      expect(screen.queryByText('管理鉴权暂时不可用，请稍后重试')).not.toBeNull()
    })

    expect(screen.queryByText('Protected area')).toBeNull()
    expect(screen.queryByText('Login')).not.toBeNull()
  })

  it('does not treat stored admin key as authenticated when health check returns service unavailable', async () => {
    localStorage.setItem('admin_key', 'saved-secret')

    fetchMock.mockImplementation(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const headers = new Headers(init?.headers)
      if (headers.get('X-Admin-Key') === 'saved-secret') {
        return new Response(JSON.stringify({
          error: '管理鉴权暂时不可用，请稍后重试',
        }), {
          status: 503,
          headers: { 'Content-Type': 'application/json' },
        })
      }

      return new Response(JSON.stringify({ error: 'Invalid admin secret' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    render(
      <AuthGate>
        <ProtectedContent>Protected area</ProtectedContent>
      </AuthGate>,
    )

    await waitFor(() => {
      expect(screen.queryByText('管理鉴权暂时不可用，请稍后重试')).not.toBeNull()
    })

    expect(localStorage.getItem('admin_key')).toBe('saved-secret')
    expect(screen.queryByText('Protected area')).toBeNull()
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/health', expect.objectContaining({
      headers: expect.any(Headers),
    }))
  })
})
